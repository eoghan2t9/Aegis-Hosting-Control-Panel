//go:build !linux

package svc

import "errors"

// runAsAccount needs Linux's per-thread filesystem identity (setfsuid). On any
// other OS it fails closed rather than quietly doing the I/O with the panel's
// own (root) privileges.
func runAsAccount(name string, fn func() error) error {
	return errors.New("per-account file access is only supported on Linux")
}
