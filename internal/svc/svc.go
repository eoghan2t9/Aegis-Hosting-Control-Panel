// Package svc implements the Aegis services: the real system integration that
// provisions web servers, PHP pools, DNS, SSL, databases, FTP, backups and
// reports system resource usage.
//
// Services run as root on the host (or inside the development container).
// Every mutating operation is designed to be idempotent and to fail loudly
// with actionable errors.
package svc

import (
	"bufio"
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"math/big"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// Exec runs a command and returns trimmed stdout/stderr. Errors include the
// command and stderr so API/CLI layers can surface them directly.
func Exec(ctx context.Context, name string, args ...string) (string, error) {
	return ExecWithEnv(ctx, nil, name, args...)
}

func ExecWithEnv(ctx context.Context, env []string, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		return strings.TrimSpace(string(out)), fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// ExecStream runs a command, invoking onLine for every line of combined
// stdout+stderr as it's produced instead of only returning it once the
// command finishes (like Exec/ExecWithEnv) — used to stream long-running
// output (e.g. apt-get) live to the panel over a WebSocket.
func ExecStream(ctx context.Context, env []string, onLine func(line string), name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = env
	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw
	if err := cmd.Start(); err != nil {
		_ = pw.Close()
		return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	scanDone := make(chan struct{})
	go func() {
		defer close(scanDone)
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 64*1024), 1<<20)
		for sc.Scan() {
			if onLine != nil {
				onLine(sc.Text())
			}
		}
	}()
	waitErr := cmd.Wait()
	_ = pw.Close()
	<-scanDone
	if waitErr != nil {
		return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), waitErr)
	}
	return nil
}

// ExecQuiet runs a command ignoring failures (used for best-effort detection).
func ExecQuiet(ctx context.Context, name string, args ...string) string {
	out, _ := Exec(ctx, name, args...)
	return out
}

// RunTimeout runs a command with a bounded context.
func RunTimeout(timeout time.Duration, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return Exec(ctx, name, args...)
}

// LookPath reports whether a binary exists on PATH.
func LookPath(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// ServiceRunning checks systemd/runit/init status; returns true when the
// service appears active. Best-effort: a missing systemctl returns false.
func ServiceRunning(service string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, _ := Exec(ctx, "systemctl", "is-active", service)
	if out == "active" {
		return true
	}
	// Fallback: check pidfiles via pgrep (containers / non-systemd hosts).
	return ProcessRunning(service)
}

// ProcessRunning reports whether any process cmdline matches the (regexp)
// pattern, e.g. "php-fpm: master process \(/etc/php/8.4/" for a versioned
// php-fpm master. Used where systemd is absent (containers, chroots).
func ProcessRunning(pattern string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if !LookPath("pgrep") {
		return false
	}
	out, _ := Exec(ctx, "pgrep", "-f", pattern)
	return strings.TrimSpace(out) != ""
}

// RandomString returns a URL-safe random string of n bytes (entropy 8n bits).
func RandomString(n int) (string, error) {
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	var sb strings.Builder
	max := big.NewInt(int64(len(chars)))
	for i := 0; i < n; i++ {
		idx, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		sb.WriteByte(chars[idx.Int64()])
	}
	return sb.String(), nil
}

// RandomPassword returns a strong 20-char password guaranteed to contain
// upper, lower and digits.
func RandomPassword() (string, error) {
	for i := 0; i < 10; i++ {
		p, err := RandomString(20)
		if err != nil {
			return "", err
		}
		if strings.ContainsAny(p, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") &&
			strings.ContainsAny(p, "abcdefghijklmnopqrstuvwxyz") &&
			strings.ContainsAny(p, "0123456789") {
			return p, nil
		}
	}
	return "", fmt.Errorf("could not generate password")
}

// --- validation --------------------------------------------------------------

var (
	usernameRe = regexp.MustCompile(`^[a-z][a-z0-9_]{2,29}$`)
	domainRe   = regexp.MustCompile(`^(?i)[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$`)
	dbNameRe   = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,63}$`)
)

// ValidUsername checks panel usernames (also used for system accounts).
func ValidUsername(u string) bool { return usernameRe.MatchString(u) }

// ValidDomain validates a hostname (allows IDN as punycode only).
func ValidDomain(d string) bool {
	d = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(d)), ".")
	return len(d) <= 253 && domainRe.MatchString(d)
}

// ValidDBName checks database names are safe to interpolate into SQL DDL.
func ValidDBName(n string) bool { return dbNameRe.MatchString(n) }

// NormalizeDomain lowercases and strips the trailing dot.
func NormalizeDomain(d string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(d), "."))
}

// IsValidIPv4 reports whether s parses as an IPv4 address.
func IsValidIPv4(s string) bool {
	parts := strings.Split(s, ".")
	if len(parts) != 4 {
		return false
	}
	for _, p := range parts {
		if len(p) == 0 || len(p) > 3 {
			return false
		}
		n := 0
		for _, c := range p {
			if c < '0' || c > '9' {
				return false
			}
			n = n*10 + int(c-'0')
		}
		if n > 255 {
			return false
		}
	}
	return true
}

// QuoteSQL single-quotes and escapes a literal for use inside SQL strings.
func QuoteSQL(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
