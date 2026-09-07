package svc

import (
	"context"
	"errors"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"aegis/internal/store"
)

// APITokens issues and verifies scoped, expiring credentials for scripting
// against the panel — an alternative to the JWT login flow for automation.
type APITokens struct {
	Store *store.Store
}

func NewAPITokens(st *store.Store) *APITokens {
	return &APITokens{Store: st}
}

const tokenPrefix = "aegis_"

// Create mints a new token for user, returning the raw token exactly once —
// only its bcrypt hash is ever persisted.
func (a *APITokens) Create(ctx context.Context, user *store.User, label string, ttl time.Duration) (*store.APIToken, string, error) {
	raw, err := RandomString(32)
	if err != nil {
		return nil, "", err
	}
	raw = tokenPrefix + raw
	hash, err := bcrypt.GenerateFromPassword([]byte(raw), bcrypt.DefaultCost)
	if err != nil {
		return nil, "", err
	}
	t := &store.APIToken{UserID: user.ID, Label: label, TokenHash: string(hash), Scopes: "*"}
	if ttl > 0 {
		exp := time.Now().UTC().Add(ttl)
		t.ExpiresAt = &exp
	}
	if err := a.Store.CreateAPIToken(ctx, t); err != nil {
		return nil, "", err
	}
	return t, raw, nil
}

// Verify checks a presented raw token and returns the user it belongs to.
// Bcrypt hashes can't be looked up by index, so this compares against every
// active token — fine at the scale a single panel issues tokens.
func (a *APITokens) Verify(ctx context.Context, raw string) (*store.User, error) {
	if !strings.HasPrefix(raw, tokenPrefix) {
		return nil, errors.New("not an api token")
	}
	tokens, err := a.Store.AllActiveAPITokens(ctx)
	if err != nil {
		return nil, err
	}
	for _, t := range tokens {
		if bcrypt.CompareHashAndPassword([]byte(t.TokenHash), []byte(raw)) == nil {
			_ = a.Store.TouchAPIToken(ctx, t.ID)
			return a.Store.GetUserByID(ctx, t.UserID)
		}
	}
	return nil, errors.New("invalid token")
}

func (a *APITokens) List(ctx context.Context, userID int64) ([]*store.APIToken, error) {
	return a.Store.ListAPITokens(ctx, userID)
}

func (a *APITokens) Delete(ctx context.Context, id int64) error {
	return a.Store.DeleteAPIToken(ctx, id)
}
