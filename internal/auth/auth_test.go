package auth

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"aegis/internal/store"
)

func newTestManager(t *testing.T) (*Manager, *store.User) {
	t.Helper()
	st, err := store.New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	mgr, err := New("a-test-secret-thats-long-enough", st, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	u := &store.User{Username: "alice", Email: "alice@example.com", PasswordHash: hash, Role: store.RoleUser, Status: store.StatusActive}
	if err := st.CreateUser(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	return mgr, u
}

func TestLoginWithoutTOTPIsUnaffected(t *testing.T) {
	mgr, u := newTestManager(t)
	ctx := context.Background()
	got, token, err := mgr.Login(ctx, u.Username, "correct horse battery staple", "127.0.0.1")
	if err != nil {
		t.Fatalf("Login() error = %v, want nil (account never enrolled in 2FA)", err)
	}
	if token == "" || got.Username != u.Username {
		t.Fatalf("Login() = %v, %q, want a real session for %s", got, token, u.Username)
	}
	if _, _, err := mgr.Verify(ctx, token); err != nil {
		t.Fatalf("token from a non-2FA login should verify: %v", err)
	}
}

func TestFullTOTPEnrollAndLoginFlow(t *testing.T) {
	mgr, u := newTestManager(t)
	ctx := context.Background()

	// 1. Enroll: secret issued, but login is still password-only until confirmed.
	secret, uri, err := mgr.BeginTOTPEnroll(ctx, u.ID, u.Username)
	if err != nil {
		t.Fatalf("BeginTOTPEnroll: %v", err)
	}
	if secret == "" || uri == "" {
		t.Fatalf("BeginTOTPEnroll returned empty secret/uri")
	}
	if _, _, err := mgr.Login(ctx, u.Username, "correct horse battery staple", "127.0.0.1"); err != nil {
		t.Fatalf("Login during pending (unconfirmed) enrollment should still succeed directly: %v", err)
	}

	// 2. Confirm with a real code from the pending secret.
	code, err := totpCodeAt(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	backupCodes, err := mgr.ConfirmTOTPEnroll(ctx, u.ID, code)
	if err != nil {
		t.Fatalf("ConfirmTOTPEnroll: %v", err)
	}
	if len(backupCodes) != totpBackupCodes {
		t.Fatalf("got %d backup codes, want %d", len(backupCodes), totpBackupCodes)
	}

	// 3. Login now must stop at the challenge, not a session.
	gotUser, challenge, err := mgr.Login(ctx, u.Username, "correct horse battery staple", "127.0.0.1")
	if !errors.Is(err, ErrTOTPRequired) {
		t.Fatalf("Login() error = %v, want ErrTOTPRequired", err)
	}
	if gotUser != nil {
		t.Fatalf("Login() user = %v, want nil while 2FA is pending", gotUser)
	}
	if challenge == "" {
		t.Fatalf("Login() returned an empty challenge")
	}
	// The challenge itself must not work as a session token.
	if _, _, err := mgr.Verify(ctx, challenge); err == nil {
		t.Fatalf("a totp challenge id must not verify as a session token")
	}

	// 4. Wrong code must fail without consuming the challenge, right code
	// (fresh, since time may have ticked over a step) must succeed.
	if _, _, err := mgr.VerifyTOTPLogin(ctx, challenge, "000000", "127.0.0.1"); err == nil {
		t.Fatalf("VerifyTOTPLogin accepted an arbitrary wrong code")
	}
	freshCode, err := totpCodeAt(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	finalUser, sessionToken, err := mgr.VerifyTOTPLogin(ctx, challenge, freshCode, "127.0.0.1")
	if err != nil {
		t.Fatalf("VerifyTOTPLogin with the correct code: %v", err)
	}
	if finalUser.Username != u.Username || sessionToken == "" {
		t.Fatalf("VerifyTOTPLogin returned %v, %q", finalUser, sessionToken)
	}
	if _, _, err := mgr.Verify(ctx, sessionToken); err != nil {
		t.Fatalf("session token from VerifyTOTPLogin should verify: %v", err)
	}

	// 5. The challenge is single-use.
	if _, _, err := mgr.VerifyTOTPLogin(ctx, challenge, freshCode, "127.0.0.1"); err == nil {
		t.Fatalf("a spent totp challenge must not be reusable")
	}
}

func TestBackupCodeLoginIsSingleUse(t *testing.T) {
	mgr, u := newTestManager(t)
	ctx := context.Background()
	secret, _, err := mgr.BeginTOTPEnroll(ctx, u.ID, u.Username)
	if err != nil {
		t.Fatal(err)
	}
	code, err := totpCodeAt(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	backupCodes, err := mgr.ConfirmTOTPEnroll(ctx, u.ID, code)
	if err != nil {
		t.Fatal(err)
	}
	first := backupCodes[0]

	_, challenge, err := mgr.Login(ctx, u.Username, "correct horse battery staple", "127.0.0.1")
	if !errors.Is(err, ErrTOTPRequired) {
		t.Fatalf("Login() error = %v, want ErrTOTPRequired", err)
	}
	if _, _, err := mgr.VerifyTOTPLogin(ctx, challenge, first, "127.0.0.1"); err != nil {
		t.Fatalf("VerifyTOTPLogin with a valid backup code: %v", err)
	}

	_, challenge2, err := mgr.Login(ctx, u.Username, "correct horse battery staple", "127.0.0.1")
	if !errors.Is(err, ErrTOTPRequired) {
		t.Fatal(err)
	}
	if _, _, err := mgr.VerifyTOTPLogin(ctx, challenge2, first, "127.0.0.1"); err == nil {
		t.Fatalf("a spent backup code must not be reusable")
	}
}

func TestDisableTOTPRequiresCorrectPassword(t *testing.T) {
	mgr, u := newTestManager(t)
	ctx := context.Background()
	secret, _, err := mgr.BeginTOTPEnroll(ctx, u.ID, u.Username)
	if err != nil {
		t.Fatal(err)
	}
	code, err := totpCodeAt(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.ConfirmTOTPEnroll(ctx, u.ID, code); err != nil {
		t.Fatal(err)
	}

	if err := mgr.DisableTOTP(ctx, u.ID, "wrong password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("DisableTOTP with a wrong password = %v, want ErrInvalidCredentials", err)
	}
	// Still enabled — a wrong password must not have disabled it.
	if _, _, err := mgr.Login(ctx, u.Username, "correct horse battery staple", "127.0.0.1"); !errors.Is(err, ErrTOTPRequired) {
		t.Fatalf("2FA should still be required after a failed disable attempt, got %v", err)
	}

	if err := mgr.DisableTOTP(ctx, u.ID, "correct horse battery staple"); err != nil {
		t.Fatalf("DisableTOTP with the correct password: %v", err)
	}
	if _, _, err := mgr.Login(ctx, u.Username, "correct horse battery staple", "127.0.0.1"); err != nil {
		t.Fatalf("Login should be password-only again after DisableTOTP: %v", err)
	}
}
