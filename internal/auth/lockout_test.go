package auth

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

const goodPassword = "correct horse battery staple"

// Reproduced live: 12 wrong guesses, each with a different X-Forwarded-For, were
// all accepted for checking. Fixing how the client address is derived stops the
// header trick, but an attacker who really has many addresses must be limited
// too: the count has to follow the username, not only (username, address).
func TestLockoutFollowsTheUsernameAcrossAddresses(t *testing.T) {
	mgr, u := newTestManager(t)
	ctx := context.Background()
	for i := 0; i < userLockoutThreshold; i++ {
		_, _, err := mgr.Login(ctx, u.Username, "wrong-guess", fmt.Sprintf("198.51.100.%d", i+1))
		if !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("guess %d: err = %v, want ErrInvalidCredentials", i+1, err)
		}
	}
	// A fresh address has made no failures itself, but the account is under attack.
	_, _, err := mgr.Login(ctx, u.Username, goodPassword, "203.0.113.200")
	if !errors.Is(err, ErrLockedOut) {
		t.Errorf("even the correct password from a new address: err = %v, want ErrLockedOut", err)
	}
}

// The distributed limit must not let an attacker lock the real owner out of
// their own panel: an address that has logged in successfully keeps working.
func TestOwnersUsualAddressIsNotLockedOutByStrangers(t *testing.T) {
	mgr, u := newTestManager(t)
	ctx := context.Background()
	home := "203.0.113.50"
	if _, _, err := mgr.Login(ctx, u.Username, goodPassword, home); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < userLockoutThreshold; i++ {
		_, _, _ = mgr.Login(ctx, u.Username, "wrong-guess", fmt.Sprintf("198.51.100.%d", i+1))
	}
	if _, _, err := mgr.Login(ctx, u.Username, goodPassword, home); err != nil {
		t.Errorf("the owner's usual address was locked out by a stranger's guesses: %v", err)
	}
	if _, _, err := mgr.Login(ctx, u.Username, goodPassword, "203.0.113.99"); !errors.Is(err, ErrLockedOut) {
		t.Errorf("an unknown address should still be locked out: %v", err)
	}
}

// ...but a known address is still bound by its own per-address limit.
func TestKnownAddressStillHasItsOwnLimit(t *testing.T) {
	mgr, u := newTestManager(t)
	ctx := context.Background()
	home := "203.0.113.50"
	if _, _, err := mgr.Login(ctx, u.Username, goodPassword, home); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < lockoutThreshold; i++ {
		_, _, _ = mgr.Login(ctx, u.Username, "wrong-guess", home)
	}
	if _, _, err := mgr.Login(ctx, u.Username, goodPassword, home); !errors.Is(err, ErrLockedOut) {
		t.Errorf("5 wrong guesses from one address should lock it: %v", err)
	}
}

// Password spraying makes one guess per account, so no per-username count ever
// fires. The address itself must be limited.
func TestPasswordSprayingFromOneAddressIsBlocked(t *testing.T) {
	mgr, u := newTestManager(t)
	ctx := context.Background()
	sprayer := "198.51.100.77"
	for i := 0; i < ipLockoutThreshold; i++ {
		_, _, _ = mgr.Login(ctx, fmt.Sprintf("user%02d", i), "Summer2026!", sprayer)
	}
	if _, _, err := mgr.Login(ctx, u.Username, goodPassword, sprayer); !errors.Is(err, ErrLockedOut) {
		t.Errorf("a sprayer's next attempt: err = %v, want ErrLockedOut", err)
	}
	// Other addresses are unaffected.
	if _, _, err := mgr.Login(ctx, u.Username, goodPassword, "203.0.113.8"); err != nil {
		t.Errorf("an unrelated address was affected: %v", err)
	}
}

// The 2FA step must be limited the same way, or the code can be brute-forced
// from many addresses within the challenge's lifetime.
func TestTOTPStepSharesTheLockout(t *testing.T) {
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
	_, challenge, err := mgr.Login(ctx, u.Username, goodPassword, "203.0.113.1")
	if !errors.Is(err, ErrTOTPRequired) {
		t.Fatalf("err = %v, want ErrTOTPRequired", err)
	}
	for i := 0; i < userLockoutThreshold; i++ {
		_, _, _ = mgr.VerifyTOTPLogin(ctx, challenge, "000000", fmt.Sprintf("198.51.100.%d", i+1))
	}
	good, _ := totpCodeAt(secret, time.Now())
	if _, _, err := mgr.VerifyTOTPLogin(ctx, challenge, good, "203.0.113.222"); !errors.Is(err, ErrLockedOut) {
		t.Errorf("a correct code from a new address after a distributed attack: err = %v, want ErrLockedOut", err)
	}
}

// An unknown username used to return without touching bcrypt, so the response
// time revealed which usernames exist.
func TestUnknownUserCostsAsMuchAsAKnownOne(t *testing.T) {
	mgr, _ := newTestManager(t)
	ctx := context.Background()
	start := time.Now()
	_, _, err := mgr.Login(ctx, "nobody-here", "whatever", "203.0.113.5")
	elapsed := time.Since(start)
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("err = %v", err)
	}
	// A bcrypt comparison at the default cost takes tens of milliseconds; skipping
	// it takes microseconds. The bound is deliberately far below the real cost.
	if elapsed < 10*time.Millisecond {
		t.Errorf("unknown-user login took %v: bcrypt was skipped", elapsed)
	}
}

// Changing the password or turning off 2FA re-checks the acting user's own
// password. Guessing it with a stolen token must hit the same lockout as guessing
// it at the login form.
func TestConfirmPasswordCountsTowardTheLockout(t *testing.T) {
	mgr, u := newTestManager(t)
	ctx := context.Background()
	ip := "198.51.100.30"

	if err := mgr.ConfirmPassword(ctx, u, goodPassword, ip); err != nil {
		t.Fatalf("the correct password was refused: %v", err)
	}
	if err := mgr.ConfirmPassword(ctx, u, "", ip); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("an empty password: err = %v, want ErrInvalidCredentials", err)
	}
	for i := 0; i < lockoutThreshold; i++ {
		if err := mgr.ConfirmPassword(ctx, u, "wrong-guess", ip); err == nil {
			t.Fatal("a wrong password was accepted")
		}
	}
	if err := mgr.ConfirmPassword(ctx, u, goodPassword, ip); !errors.Is(err, ErrLockedOut) {
		t.Errorf("after repeated wrong guesses: err = %v, want ErrLockedOut", err)
	}
	// And it shares the login lockout, so the login form is closed to that address too.
	if _, _, err := mgr.Login(ctx, u.Username, goodPassword, ip); !errors.Is(err, ErrLockedOut) {
		t.Errorf("login after guessing via ConfirmPassword: err = %v, want ErrLockedOut", err)
	}
}
