// TOTP (RFC 6238) two-factor auth — deliberately stdlib-only rather than a
// third-party dependency, since the algorithm is small, fully specified,
// and this is exactly the kind of code where "we can read every line of
// what actually runs" is worth more than a library's convenience.
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const (
	totpDigits = 6
	totpPeriod = 30 * time.Second
	// totpSkew allows the previous and next 30s step to also validate,
	// tolerating ordinary clock drift between this server and the phone
	// running the authenticator app — without it, a few seconds of drift
	// either way makes every code fail unpredictably.
	totpSkew = 1
)

// GenerateTOTPSecret returns a fresh base32 (no padding) secret suitable for
// an authenticator app — 20 random bytes, the size Google
// Authenticator/Authy/1Password etc. all expect.
func GenerateTOTPSecret() (string, error) {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b), nil
}

// TOTPURI builds the otpauth:// URI an authenticator app's "scan a QR code"
// or "enter a setup key" flow expects. accountName is shown in the app
// (typically username or username@panel) to distinguish multiple accounts.
func TOTPURI(secret, accountName, issuer string) string {
	label := url.PathEscape(issuer) + ":" + url.PathEscape(accountName)
	q := url.Values{
		"secret":    {secret},
		"issuer":    {issuer},
		"algorithm": {"SHA1"},
		"digits":    {fmt.Sprintf("%d", totpDigits)},
		"period":    {fmt.Sprintf("%d", int(totpPeriod.Seconds()))},
	}
	return "otpauth://totp/" + label + "?" + q.Encode()
}

// totpCodeAt computes the 6-digit code for secret at the given time step.
func totpCodeAt(secret string, t time.Time) (string, error) {
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(strings.TrimSpace(secret)))
	if err != nil {
		return "", err
	}
	counter := uint64(t.Unix()) / uint64(totpPeriod.Seconds())
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], counter)

	mac := hmac.New(sha1.New, key)
	mac.Write(buf[:])
	sum := mac.Sum(nil)

	// Dynamic truncation (RFC 4226 §5.3).
	offset := sum[len(sum)-1] & 0x0f
	code := (uint32(sum[offset]&0x7f) << 24) | (uint32(sum[offset+1]) << 16) |
		(uint32(sum[offset+2]) << 8) | uint32(sum[offset+3])
	mod := uint32(1)
	for i := 0; i < totpDigits; i++ {
		mod *= 10
	}
	return fmt.Sprintf("%0*d", totpDigits, code%mod), nil
}

// ValidateTOTPCode reports whether code matches secret at the current time
// step or either adjacent step (see totpSkew). Comparison is constant-time
// per candidate to avoid a timing side-channel on the digits.
func ValidateTOTPCode(secret, code string) bool {
	code = strings.TrimSpace(code)
	if len(code) != totpDigits {
		return false
	}
	now := time.Now()
	for skew := -totpSkew; skew <= totpSkew; skew++ {
		want, err := totpCodeAt(secret, now.Add(time.Duration(skew)*totpPeriod))
		if err != nil {
			return false
		}
		if subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
			return true
		}
	}
	return false
}

// backupCodeAlphabet excludes visually ambiguous characters (0/O, 1/I/L) —
// these get read back off a screen or a piece of paper by a human.
const backupCodeAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

// GenerateBackupCodes returns n one-time recovery codes, formatted
// "XXXXX-XXXXX" for readability. Callers must bcrypt-hash each before
// persisting — these plaintext values are shown to the user exactly once.
func GenerateBackupCodes(n int) ([]string, error) {
	out := make([]string, n)
	for i := range out {
		raw := make([]byte, 10)
		if _, err := rand.Read(raw); err != nil {
			return nil, err
		}
		var sb strings.Builder
		for j, b := range raw {
			if j == 5 {
				sb.WriteByte('-')
			}
			sb.WriteByte(backupCodeAlphabet[int(b)%len(backupCodeAlphabet)])
		}
		out[i] = sb.String()
	}
	return out, nil
}
