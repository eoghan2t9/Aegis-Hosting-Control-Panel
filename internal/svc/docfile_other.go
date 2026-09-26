//go:build !linux

package svc

import (
	"errors"
	"os"
)

var errNoDocFile = errors.New("serving customer files is only supported on Linux")

// statAs and openDocFile need per-thread filesystem identity and /proc, which
// only Linux has; elsewhere they fail closed instead of touching files as root.
func statAs(owner, path string) (os.FileInfo, error) { return nil, errNoDocFile }

func openDocFile(owner, file string) (*os.File, os.FileInfo, error) {
	return nil, nil, errNoDocFile
}
