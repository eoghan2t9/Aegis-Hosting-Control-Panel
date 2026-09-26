//go:build linux

package svc

import (
	"errors"
	"fmt"
	"runtime"
	"syscall"
	"unsafe"
)

// runAsAccount runs fn with the calling OS thread's *filesystem* identity
// (fsuid, fsgid and supplementary groups) switched to the named system
// account, then restores it.
//
// The panel runs as root, and root ignores file permissions, so a path check
// like Files.Resolve is the only thing standing between a customer's symlink
// and the rest of the host. Doing the actual I/O as the customer turns that
// weak, race-prone check into defence in depth: whatever a symlink points at,
// the kernel refuses anything the customer could not open themselves. This is
// the technique NFS and Samba servers use. Dropping fsuid from 0 to non-zero
// also clears the filesystem capabilities (CAP_DAC_OVERRIDE, CAP_CHOWN,
// CAP_FOWNER, CAP_FSETID, ...), so files the customer creates are owned by them
// and cannot be given a root owner or a setuid-root mode.
//
// Only the calling thread changes (that is why fn runs on a locked OS thread),
// and only its filesystem identity, so the panel's own database and network
// access on other goroutines is untouched. fn must do its file I/O on the
// calling goroutine and must not depend on the panel's own files.
func runAsAccount(name string, fn func() error) error {
	a, err := lookupAccount(name)
	if err != nil {
		return fmt.Errorf("no system account for %q: %w", name, err)
	}
	return runAsIDs(a.uid, a.gid, a.groups, fn)
}

func runAsIDs(uid, gid uint32, groups []uint32, fn func() error) (err error) {
	if uid == 0 || gid == 0 {
		return errors.New("refusing to run file operations as uid/gid 0")
	}
	runtime.LockOSThread()
	// The thread is only released when every identity is verifiably back to
	// what it was. Otherwise it stays locked, and the Go runtime discards the
	// thread when this goroutine exits, so a half-restored thread is never
	// reused by another request.
	restored := false
	defer func() {
		if restored {
			runtime.UnlockOSThread()
		}
	}()

	pg, gerr := syscall.Getgroups()
	if gerr != nil {
		restored = true // nothing has been changed yet
		return gerr
	}
	prevGroups := make([]uint32, len(pg))
	for i, g := range pg {
		prevGroups[i] = uint32(g)
	}
	prevFsuid, prevFsgid := getFsuid(), getFsgid()

	defer func() {
		// Regain root first: setfsgid and setgroups both need it.
		setFsuid(prevFsuid)
		setFsgid(prevFsgid)
		_ = setGroupsThread(prevGroups)
		restored = getFsuid() == prevFsuid && getFsgid() == prevFsgid
	}()

	if e := setGroupsThread(groups); e != nil {
		return fmt.Errorf("switch groups: %w", e)
	}
	setFsgid(gid)
	setFsuid(uid) // last: this is the step that drops the capabilities
	if getFsuid() != uid || getFsgid() != gid {
		return errors.New("could not switch the filesystem identity (is the panel running as root?)")
	}
	return fn()
}

func getFsuid() uint32 {
	r, _, _ := syscall.RawSyscall(syscall.SYS_SETFSUID, ^uintptr(0), 0, 0) // -1 only reports the current value
	return uint32(r)
}

func getFsgid() uint32 {
	r, _, _ := syscall.RawSyscall(syscall.SYS_SETFSGID, ^uintptr(0), 0, 0)
	return uint32(r)
}

// setFsuid/setFsgid cannot report failure (the syscall returns the previous
// value either way), so callers verify with getFsuid/getFsgid.
func setFsuid(id uint32) { syscall.RawSyscall(syscall.SYS_SETFSUID, uintptr(id), 0, 0) }
func setFsgid(id uint32) { syscall.RawSyscall(syscall.SYS_SETFSGID, uintptr(id), 0, 0) }

// setGroupsThread sets the supplementary groups of the calling thread only.
// syscall.Setgroups is deliberately not used: it applies to every thread in
// the process.
func setGroupsThread(groups []uint32) error {
	var p unsafe.Pointer
	if len(groups) > 0 {
		p = unsafe.Pointer(&groups[0])
	}
	_, _, e := syscall.RawSyscall(syscall.SYS_SETGROUPS, uintptr(len(groups)), uintptr(p), 0)
	if e != 0 {
		return e
	}
	return nil
}
