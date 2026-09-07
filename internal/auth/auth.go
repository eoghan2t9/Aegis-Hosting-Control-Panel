// Package auth provides JWT-based authentication, password hashing, role
// enforcement and admin impersonation for the Aegis panel.
//
// Tokens are signed with HMAC-SHA256 using a secret derived from the panel
// secret file. Each token carries a session id recorded in the store so
// sessions can be revoked (logout, password reset, suspension).
package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"

	"aegis/internal/store"
)

// Errors returned by the auth package.
var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrInvalidToken       = errors.New("invalid or expired token")
	ErrSuspended          = errors.New("account is suspended")
	ErrForbidden          = errors.New("forbidden")
)

// Claims is the JWT payload.
type Claims struct {
	Username string `json:"username"`
	Role     string `json:"role"`
	// Impersonator is the admin user id when this token was minted via
	// "login as user". Zero when the token is a normal login.
	Impersonator int64  `json:"imp,omitempty"`
	SessionID    string `json:"sid,omitempty"`
	jwt.RegisteredClaims
}

// Manager issues and verifies tokens.
type Manager struct {
	secret []byte
	store  *store.Store
	ttl    time.Duration
}

// New returns a Manager. secret must be at least 16 bytes.
func New(secret string, st *store.Store, ttl time.Duration) (*Manager, error) {
	if len(secret) < 16 {
		return nil, errors.New("auth: secret too short")
	}
	return &Manager{secret: []byte(secret), store: st, ttl: ttl}, nil
}

// HashPassword returns a bcrypt hash with default cost.
func HashPassword(password string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(b), err
}

// CheckPassword compares a plaintext password with a bcrypt hash.
func CheckPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// HashPassword implements the password setter interface used by the backup
// service when recreating accounts.
func (m *Manager) HashPassword(password string) (string, error) {
	return HashPassword(password)
}

// Login authenticates a username/password pair, creates a session and returns
// the user plus a signed token.
func (m *Manager) Login(ctx context.Context, username, password string) (*store.User, string, error) {
	u, err := m.store.GetUserByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, "", ErrInvalidCredentials
		}
		return nil, "", err
	}
	if !CheckPassword(u.PasswordHash, password) {
		return nil, "", ErrInvalidCredentials
	}
	if u.Status == store.StatusSuspended {
		return nil, "", ErrSuspended
	}
	sid, err := newSessionID()
	if err != nil {
		return nil, "", err
	}
	exp := time.Now().Add(m.ttl)
	if err := m.store.CreateSession(ctx, sid, u.ID, exp); err != nil {
		return nil, "", err
	}
	token, err := m.mint(u, sid, 0)
	if err != nil {
		return nil, "", err
	}
	return u, token, nil
}

// Impersonate mints a token that acts as target but is attributable to admin
// via the Impersonator claim. Used for the "login as user" support feature.
func (m *Manager) Impersonate(ctx context.Context, adminID, targetID int64) (string, error) {
	admin, err := m.store.GetUserByID(ctx, adminID)
	if err != nil {
		return "", err
	}
	if admin.Role != store.RoleAdmin {
		return "", ErrForbidden
	}
	target, err := m.store.GetUserByID(ctx, targetID)
	if err != nil {
		return "", err
	}
	if target.Status == store.StatusSuspended {
		return "", ErrSuspended
	}
	sid, err := newSessionID()
	if err != nil {
		return "", err
	}
	exp := time.Now().Add(m.ttl)
	if err := m.store.CreateSession(ctx, sid, target.ID, exp); err != nil {
		return "", err
	}
	return m.mint(target, sid, adminID)
}

// Unimpersonate ends a support session: revokes the impersonated session and
// mints a fresh token for the original admin (the Impersonator claim), so
// the frontend can drop straight back into the admin's own dashboard instead
// of being left with no valid token at all.
func (m *Manager) Unimpersonate(ctx context.Context, token string) (string, error) {
	claims, err := m.Parse(token)
	if err != nil {
		return "", ErrInvalidToken
	}
	_ = m.store.DeleteSession(ctx, claims.SessionID)
	if claims.Impersonator == 0 {
		return "", errors.New("not an impersonated session")
	}
	admin, err := m.store.GetUserByID(ctx, claims.Impersonator)
	if err != nil {
		return "", err
	}
	sid, err := newSessionID()
	if err != nil {
		return "", err
	}
	if err := m.store.CreateSession(ctx, sid, admin.ID, time.Now().Add(m.ttl)); err != nil {
		return "", err
	}
	return m.mint(admin, sid, 0)
}

func (m *Manager) mint(u *store.User, sessionID string, impersonator int64) (string, error) {
	now := time.Now()
	claims := Claims{
		Username:     u.Username,
		Role:         u.Role,
		Impersonator: impersonator,
		SessionID:    sessionID,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   fmt.Sprintf("%d", u.ID),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(m.ttl)),
			Issuer:    "aegis",
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(m.secret)
}

// Parse validates the token signature/expiry and returns its claims.
func (m *Manager) Parse(token string) (*Claims, error) {
	claims := &Claims{}
	_, err := jwt.ParseWithClaims(token, claims, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, ErrInvalidToken
		}
		return m.secret, nil
	}, jwt.WithValidMethods([]string{"HS256"}), jwt.WithIssuer("aegis"))
	if err != nil {
		return nil, ErrInvalidToken
	}
	return claims, nil
}

// Verify checks the token is valid, the session exists/not expired and the
// account is active. Returns the session owner (the impersonated user when
// impersonating).
func (m *Manager) Verify(ctx context.Context, token string) (*Claims, *store.User, error) {
	claims, err := m.Parse(token)
	if err != nil {
		return nil, nil, err
	}
	uid, err := m.store.GetSessionUser(ctx, claims.SessionID)
	if err != nil {
		return nil, nil, ErrInvalidToken
	}
	u, err := m.store.GetUserByID(ctx, uid)
	if err != nil {
		return nil, nil, ErrInvalidToken
	}
	if u.Status == store.StatusSuspended {
		return nil, nil, ErrSuspended
	}
	return claims, u, nil
}

// Logout revokes the session attached to the token.
func (m *Manager) Logout(ctx context.Context, token string) error {
	claims, err := m.Parse(token)
	if err != nil {
		return nil // already invalid; nothing to revoke
	}
	return m.store.DeleteSession(ctx, claims.SessionID)
}

// HasRole reports whether claims carry one of the given roles.
func HasRole(claims *Claims, roles ...string) bool {
	for _, r := range roles {
		if claims.Role == r {
			return true
		}
	}
	return false
}

// IsImpersonating reports whether claims were minted via "login as user".
func IsImpersonating(claims *Claims) bool { return claims.Impersonator > 0 }

func newSessionID() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
