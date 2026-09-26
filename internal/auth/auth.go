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
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
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
	ErrLockedOut          = errors.New("too many failed attempts — try again in a few minutes")
	// ErrTOTPRequired is Login's signal that the password checked out but
	// the account has 2FA enabled — the caller must treat the returned
	// token as a totp challenge id, not a session token, and prompt for a
	// code (see handleLogin/handleTOTPVerify).
	ErrTOTPRequired = errors.New("totp code required")
	// ErrTOTPNotEnrolled/ErrTOTPAlreadyEnabled guard the enroll/confirm/
	// disable flow against being called out of order.
	ErrTOTPNotEnrolled    = errors.New("no totp enrollment in progress")
	ErrTOTPAlreadyEnabled = errors.New("two-factor auth is already enabled")
	ErrTOTPInvalidCode    = errors.New("invalid or expired code")
)

const (
	// lockoutThreshold is failures per username *from one address*.
	lockoutThreshold = 5
	// userLockoutThreshold is failures per username from ANY address in the
	// window: the per-address count alone lets an attacker with many addresses
	// (a botnet, or many local tenants) make 5 guesses from each.
	userLockoutThreshold = 15
	// ipLockoutThreshold is failures per address across ALL usernames: password
	// spraying tries one guess on each account and never trips a per-user count.
	ipLockoutThreshold = 30
	// knownAddressWindow is how long a successful login marks an address as one
	// the account's owner really uses (see lockedOut).
	knownAddressWindow = 30 * 24 * time.Hour
	lockoutWindow      = 15 * time.Minute
	// totpChallengeTTL is deliberately short — this token only ever proves
	// "the password just checked out", not "this person is logged in", so
	// there's no reason for it to outlive the few seconds it takes to type
	// a 6-digit code.
	totpChallengeTTL = 5 * time.Minute
	totpBackupCodes  = 8
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

// dummyHash is compared against when the username does not exist, so that an
// unknown account costs the same bcrypt time as a known one. Without it the
// response time tells an attacker which usernames are real.
var dummyHash = func() string {
	h, _ := bcrypt.GenerateFromPassword([]byte("aegis-timing-equalizer"), bcrypt.DefaultCost)
	return string(h)
}()

// lockedOut reports whether a login (or a 2FA code) for username from ip should
// be refused because of recent failures. Three independent limits apply:
//
//   - username+address: the original limit,
//   - address across all usernames: password spraying,
//   - username across all addresses: distributed guessing. To stop an attacker
//     from using this to lock the real owner out of their own panel, it does not
//     apply to an address that has already logged in as this user successfully
//     within knownAddressWindow (that address is still bound by the first limit).
func (m *Manager) lockedOut(ctx context.Context, username, ip string) bool {
	since := time.Now().Add(-lockoutWindow)
	if n, err := m.store.CountRecentFailures(ctx, username, ip, since); err == nil && n >= lockoutThreshold {
		return true
	}
	if n, err := m.store.CountRecentFailuresFromIP(ctx, ip, since); err == nil && n >= ipLockoutThreshold {
		return true
	}
	if n, err := m.store.CountRecentFailuresForUser(ctx, username, since); err == nil && n >= userLockoutThreshold {
		if known, err := m.store.HasRecentSuccess(ctx, username, ip, time.Now().Add(-knownAddressWindow)); err == nil && known {
			return false
		}
		return true
	}
	return false
}

// ConfirmPassword re-verifies the acting user's OWN password before a sensitive
// action (changing the password, turning off 2FA). A bearer token alone is not
// enough for those: it may have been stolen. Wrong answers count toward the same
// lockout as failed logins, so a stolen token cannot be used to guess the
// password without limit.
func (m *Manager) ConfirmPassword(ctx context.Context, u *store.User, password, ip string) error {
	if m.lockedOut(ctx, u.Username, ip) {
		return ErrLockedOut
	}
	if password == "" || !CheckPassword(u.PasswordHash, password) {
		_ = m.store.RecordLoginAttempt(ctx, u.Username, ip, false)
		logFailedAttempt(u.Username, ip)
		return ErrInvalidCredentials
	}
	return nil
}

// HashPassword implements the password setter interface used by the backup
// service when recreating accounts.
func (m *Manager) HashPassword(password string) (string, error) {
	return HashPassword(password)
}

// Login authenticates a username/password pair, creates a session and returns
// the user plus a signed token.
func (m *Manager) Login(ctx context.Context, username, password, ip string) (*store.User, string, error) {
	if m.lockedOut(ctx, username, ip) {
		return nil, "", ErrLockedOut
	}
	u, err := m.store.GetUserByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			_ = CheckPassword(dummyHash, password) // same cost as a real account
			_ = m.store.RecordLoginAttempt(ctx, username, ip, false)
			logFailedAttempt(username, ip)
			return nil, "", ErrInvalidCredentials
		}
		return nil, "", err
	}
	if !CheckPassword(u.PasswordHash, password) {
		_ = m.store.RecordLoginAttempt(ctx, username, ip, false)
		logFailedAttempt(username, ip)
		return nil, "", ErrInvalidCredentials
	}
	if u.Status == store.StatusSuspended {
		_ = m.store.RecordLoginAttempt(ctx, username, ip, false)
		return nil, "", ErrSuspended
	}
	_ = m.store.RecordLoginAttempt(ctx, username, ip, true)
	if u.TOTPEnabled {
		// Password alone is not enough for this account — hand back a
		// short-lived challenge id instead of a session. It is NOT a valid
		// bearer token (withAuth only accepts real session ids), so a
		// caller that stops here gains nothing beyond "the password was
		// correct", exactly like any other 2FA implementation.
		cid, err := newSessionID()
		if err != nil {
			return nil, "", err
		}
		if err := m.store.CreateTOTPChallenge(ctx, cid, u.ID, time.Now().Add(totpChallengeTTL)); err != nil {
			return nil, "", err
		}
		return nil, cid, ErrTOTPRequired
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

// completeLogin is Login's tail (mint a session) factored out so
// VerifyTOTPLogin can reuse it once a 2FA code has also checked out.
func (m *Manager) completeLogin(ctx context.Context, u *store.User) (*store.User, string, error) {
	sid, err := newSessionID()
	if err != nil {
		return nil, "", err
	}
	if err := m.store.CreateSession(ctx, sid, u.ID, time.Now().Add(m.ttl)); err != nil {
		return nil, "", err
	}
	token, err := m.mint(u, sid, 0)
	if err != nil {
		return nil, "", err
	}
	return u, token, nil
}

// VerifyTOTPLogin redeems a challenge from Login (ErrTOTPRequired) with
// either a live 6-digit code or a one-time backup code, then mints a real
// session — the second half of the two-step login for a 2FA account.
// Wrong codes are recorded through the same login_attempts table password
// failures use, so TOTP brute-forcing trips the same lockout.
func (m *Manager) VerifyTOTPLogin(ctx context.Context, challenge, code, ip string) (*store.User, string, error) {
	uid, err := m.store.GetTOTPChallengeUser(ctx, challenge)
	if err != nil {
		return nil, "", ErrInvalidToken
	}
	u, err := m.store.GetUserByID(ctx, uid)
	if err != nil {
		return nil, "", ErrInvalidToken
	}
	if m.lockedOut(ctx, u.Username, ip) {
		return nil, "", ErrLockedOut
	}
	if u.Status == store.StatusSuspended {
		_ = m.store.DeleteTOTPChallenge(ctx, challenge)
		return nil, "", ErrSuspended
	}

	if ValidateTOTPCode(u.TOTPSecret, code) {
		_ = m.store.RecordLoginAttempt(ctx, u.Username, ip, true)
		_ = m.store.DeleteTOTPChallenge(ctx, challenge)
		return m.completeLogin(ctx, u)
	}
	if consumeBackupCode(ctx, m.store, u, code) {
		_ = m.store.RecordLoginAttempt(ctx, u.Username, ip, true)
		_ = m.store.DeleteTOTPChallenge(ctx, challenge)
		return m.completeLogin(ctx, u)
	}

	_ = m.store.RecordLoginAttempt(ctx, u.Username, ip, false)
	logFailedAttempt(u.Username, ip)
	return nil, "", ErrTOTPInvalidCode
}

// BeginTOTPEnroll generates a fresh secret for userID and stores it
// pending (totp_enabled stays false — see store.SetTOTPSecret) and returns
// the otpauth:// URI plus the raw secret for manual entry.
func (m *Manager) BeginTOTPEnroll(ctx context.Context, userID int64, username string) (secret, uri string, err error) {
	secret, err = GenerateTOTPSecret()
	if err != nil {
		return "", "", err
	}
	if err := m.store.SetTOTPSecret(ctx, userID, secret); err != nil {
		return "", "", err
	}
	return secret, TOTPURI(secret, username, "Aegis"), nil
}

// ConfirmTOTPEnroll completes an enrollment BeginTOTPEnroll started: the
// user must prove they can generate a real code from the pending secret
// before it's trusted for login, exactly the same way a password is
// confirmed by typing it once at signup — otherwise a typo'd or
// misconfigured authenticator app would silently lock the account out on
// the very next login. Returns the plaintext backup codes; only this
// instant, ever — only their bcrypt hashes are persisted.
func (m *Manager) ConfirmTOTPEnroll(ctx context.Context, userID int64, code string) ([]string, error) {
	u, err := m.store.GetUserByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if u.TOTPEnabled {
		return nil, ErrTOTPAlreadyEnabled
	}
	if u.TOTPSecret == "" {
		return nil, ErrTOTPNotEnrolled
	}
	if !ValidateTOTPCode(u.TOTPSecret, code) {
		return nil, ErrTOTPInvalidCode
	}
	codes, err := GenerateBackupCodes(totpBackupCodes)
	if err != nil {
		return nil, err
	}
	hashes := make([]string, len(codes))
	for i, c := range codes {
		h, err := HashPassword(c)
		if err != nil {
			return nil, err
		}
		hashes[i] = h
	}
	data, err := json.Marshal(hashes)
	if err != nil {
		return nil, err
	}
	if err := m.store.EnableTOTP(ctx, userID, string(data)); err != nil {
		return nil, err
	}
	return codes, nil
}

// DisableTOTP turns 2FA off after re-confirming the account's password —
// the settings page is reachable with nothing but a bearer token, and a
// hijacked-but-unlocked browser tab shouldn't be enough to silently strip
// an account's second factor.
func (m *Manager) DisableTOTP(ctx context.Context, userID int64, password string) error {
	u, err := m.store.GetUserByID(ctx, userID)
	if err != nil {
		return err
	}
	if !CheckPassword(u.PasswordHash, password) {
		return ErrInvalidCredentials
	}
	return m.store.DisableTOTP(ctx, userID)
}

// consumeBackupCode checks code against u's stored backup-code hashes; on a
// match it rewrites the stored list with that hash removed (single-use) and
// returns true. Kept as a free function (not a Manager method) since it
// only needs the store interface, not any Manager state.
func consumeBackupCode(ctx context.Context, st *store.Store, u *store.User, code string) bool {
	code = strings.TrimSpace(code)
	if code == "" || u.TOTPBackupCodes == "" {
		return false
	}
	var hashes []string
	if err := json.Unmarshal([]byte(u.TOTPBackupCodes), &hashes); err != nil {
		return false
	}
	for i, h := range hashes {
		if CheckPassword(h, code) {
			remaining := append(hashes[:i:i], hashes[i+1:]...)
			data, err := json.Marshal(remaining)
			if err != nil {
				return false
			}
			_ = st.SetTOTPBackupCodes(ctx, u.ID, string(data))
			return true
		}
	}
	return false
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

// authLogPath is a plain-text log of failed logins, in a fixed format a
// fail2ban filter can regex against — deliberately separate from the audit
// log (structured, DB-backed, not filesystem-tailable by an external tool).
const authLogPath = "/var/log/aegis-auth.log"

func logFailedAttempt(username, ip string) {
	f, err := os.OpenFile(authLogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return // best-effort: fail2ban integration is optional, login throttling above doesn't depend on this
	}
	defer f.Close()
	fmt.Fprintf(f, "%s aegis: authentication failure for %s from %s\n", time.Now().UTC().Format(time.RFC3339), username, ip)
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
