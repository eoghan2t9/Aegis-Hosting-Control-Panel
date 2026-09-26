package svc

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"aegis/internal/store"
)

// cmdLog records the system commands a Suspender would run, and answers
// "docker inspect" from a table so no real command ever executes.
type cmdLog struct {
	mu      sync.Mutex
	cmds    []string
	running map[string]bool // container name -> running
}

func (c *cmdLog) run(_ time.Duration, name string, args ...string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	line := name + " " + strings.Join(args, " ")
	c.cmds = append(c.cmds, line)
	if name == "docker" && len(args) > 0 && args[0] == "inspect" {
		cname := args[len(args)-1]
		r, ok := c.running[cname]
		if !ok {
			return "", errors.New("no such container")
		}
		if r {
			return "true\n", nil
		}
		return "false\n", nil
	}
	return "", nil
}

func (c *cmdLog) has(prefix string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, l := range c.cmds {
		if strings.HasPrefix(l, prefix) {
			return true
		}
	}
	return false
}

func (c *cmdLog) reset() { c.mu.Lock(); c.cmds = nil; c.mu.Unlock() }

type suspendFixture struct {
	s      *Suspender
	st     *store.Store
	cmds   *cmdLog
	user   *store.User
	boxOn  *store.Mailbox // enabled before suspension
	boxOff *store.Mailbox // disabled by the admin before suspension
	conRun *store.Container
	conOff *store.Container
	ftpOn  *store.FTPAccount
	ftpOff *store.FTPAccount
}

func newSuspendFixture(t *testing.T) *suspendFixture {
	t.Helper()
	ctx := context.Background()
	st, err := store.New(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	u := &store.User{Username: "alice", Email: "alice@example.com", PasswordHash: "h", Role: store.RoleUser, Status: store.StatusActive, HomeDir: "/home/alice"}
	must(st.CreateUser(ctx, u))
	dom := &store.Domain{UserID: u.ID, Domain: "site.example.com", DocumentRoot: "/home/alice/site/public"}
	must(st.CreateDomain(ctx, dom))
	md := &store.MailDomain{DomainID: dom.ID, Domain: dom.Domain}
	must(st.CreateMailDomain(ctx, md))

	f := &suspendFixture{st: st, user: u, cmds: &cmdLog{running: map[string]bool{}}}
	f.boxOn = &store.Mailbox{MailDomainID: md.ID, Localpart: "info", PasswordHash: "h", Enabled: true}
	f.boxOff = &store.Mailbox{MailDomainID: md.ID, Localpart: "old", PasswordHash: "h", Enabled: false}
	must(st.CreateMailbox(ctx, f.boxOn))
	must(st.CreateMailbox(ctx, f.boxOff))

	f.ftpOn = &store.FTPAccount{UserID: u.ID, Username: "alice_ftp", PasswordHash: "h", HomeDir: "/home/alice/site", Enabled: true}
	f.ftpOff = &store.FTPAccount{UserID: u.ID, Username: "alice_old", PasswordHash: "h", HomeDir: "/home/alice/old", Enabled: false}
	must(st.CreateFTPAccount(ctx, f.ftpOn))
	must(st.CreateFTPAccount(ctx, f.ftpOff))

	f.conRun = &store.Container{UserID: u.ID, Name: "web", Image: "nginx", RestartPolicy: "always"}
	f.conOff = &store.Container{UserID: u.ID, Name: "worker", Image: "busybox", RestartPolicy: "unless-stopped"}
	must(st.CreateContainer(ctx, f.conRun))
	must(st.CreateContainer(ctx, f.conOff))
	f.cmds.running[containerName(f.conRun.ID)] = true
	f.cmds.running[containerName(f.conOff.ID)] = false // stopped by the user already

	f.s = NewSuspender(nil, st, nil, nil, nil, nil, nil)
	f.s.run = f.cmds.run
	return f
}

func TestSuspendEnforcesEverywhereAndUnsuspendRestoresExactly(t *testing.T) {
	ctx := context.Background()
	f := newSuspendFixture(t)
	_ = f.st.SetSuspendedByQuota(ctx, f.user.ID, true) // e.g. it was over quota first
	if err := f.st.CreateSession(ctx, "panel-sess", f.user.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	rep, err := f.s.Suspend(ctx, f.user)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Warnings) != 0 {
		t.Errorf("unexpected warnings: %v", rep.Warnings)
	}

	// Status, and an admin suspension takes over from the quota one.
	if manual, _ := f.st.IsManuallySuspended(ctx, f.user.ID); !manual {
		t.Error("account is not manually suspended after Suspend")
	}
	if byQuota, _ := f.st.IsSuspendedByQuota(ctx, f.user.ID); byQuota {
		t.Error("the quota flag was left set on a manual suspension")
	}
	// System logins: the owner and every FTP account locked, sessions killed.
	for _, n := range []string{"alice", "alice_ftp", "alice_old"} {
		if !f.cmds.has("usermod -L " + n) {
			t.Errorf("system login %s was not locked", n)
		}
		if !f.cmds.has("pkill -KILL -u " + n) {
			t.Errorf("running processes of %s were not killed", n)
		}
	}
	// Containers: the running one stopped and stopped from restarting; the
	// already-stopped one not started or stopped, but also kept from restarting.
	runName, offName := containerName(f.conRun.ID), containerName(f.conOff.ID)
	if !f.cmds.has("docker stop "+runName) || !f.cmds.has("docker update --restart=no "+runName) {
		t.Error("the running container was not stopped and set to not restart")
	}
	if f.cmds.has("docker stop " + offName) {
		t.Error("an already-stopped container was stopped again")
	}
	if !f.cmds.has("docker update --restart=no " + offName) {
		t.Error("a stopped container with restart=unless-stopped could still restart on boot")
	}
	// Mailboxes: only the enabled one is disabled and remembered.
	boxes := func() (on, off bool) {
		md, _ := f.st.GetMailDomainByDomainID(ctx, f.boxOn.MailDomainID)
		list, _ := f.st.ListMailboxes(ctx, md.ID)
		for _, b := range list {
			switch b.Localpart {
			case "info":
				on = b.Enabled
			case "old":
				off = b.Enabled
			}
		}
		return
	}
	if info, old := boxes(); info || old {
		t.Errorf("mailboxes after suspend: info enabled=%v old enabled=%v, want both disabled", info, old)
	}
	if ids, _ := f.st.ListSuspensionActions(ctx, f.user.ID, store.SuspendKindMailbox); len(ids) != 1 || ids[0] != f.boxOn.ID {
		t.Errorf("recorded mailbox actions = %v, want only the one that was enabled", ids)
	}
	// Panel sessions revoked.
	if _, err := f.st.GetSessionUser(ctx, "panel-sess"); err == nil {
		t.Error("the panel session survived the suspension")
	}

	// --- unsuspend restores exactly what was changed ---
	f.cmds.reset()
	if _, err := f.s.Unsuspend(ctx, f.user); err != nil {
		t.Fatal(err)
	}
	if manual, _ := f.st.IsManuallySuspended(ctx, f.user.ID); manual {
		t.Error("account still suspended after Unsuspend")
	}
	if !f.cmds.has("usermod -U alice") {
		t.Error("the owner's system login was not unlocked")
	}
	if !f.cmds.has("usermod -U alice_ftp") {
		t.Error("an enabled FTP account was not unlocked")
	}
	if f.cmds.has("usermod -U alice_old") {
		t.Error("an FTP account the admin had disabled was unlocked by unsuspending")
	}
	if !f.cmds.has("docker start "+runName) || f.cmds.has("docker start "+offName) {
		t.Error("only the container suspension stopped should be started again")
	}
	if !f.cmds.has("docker update --restart=always "+runName) || !f.cmds.has("docker update --restart=unless-stopped "+offName) {
		t.Error("restart policies were not restored")
	}
	if info, old := boxes(); !info || old {
		t.Errorf("mailboxes after unsuspend: info enabled=%v old enabled=%v, want info back on and old still off", info, old)
	}
	if ids, _ := f.st.ListSuspensionActions(ctx, f.user.ID, store.SuspendKindMailbox); len(ids) != 0 {
		t.Errorf("suspension actions not cleared: %v", ids)
	}
}

// Quota suspension must not be treated as a manual one: an over-quota user
// keeps FTP so they can free space.
func TestQuotaSuspensionIsNotManual(t *testing.T) {
	ctx := context.Background()
	f := newSuspendFixture(t)
	if err := f.st.SetUserStatus(ctx, f.user.ID, store.StatusSuspended); err != nil {
		t.Fatal(err)
	}
	if err := f.st.SetSuspendedByQuota(ctx, f.user.ID, true); err != nil {
		t.Fatal(err)
	}
	if manual, _ := f.st.IsManuallySuspended(ctx, f.user.ID); manual {
		t.Error("a quota suspension counts as manual")
	}
	if f.st.IsUsernameManuallySuspended(ctx, "alice") {
		t.Error("IsUsernameManuallySuspended is true for a quota suspension")
	}
	f.s.Reconcile(ctx)
	if f.cmds.has("usermod -L") {
		t.Error("reconcile locked the logins of a quota-suspended account")
	}
}

// A suspend made outside this process (aegisctl, or an edit to the database) is
// picked up by Reconcile, and lifted when the status goes back.
func TestReconcilePicksUpOutOfBandChanges(t *testing.T) {
	ctx := context.Background()
	f := newSuspendFixture(t)

	f.s.Reconcile(ctx)
	if f.cmds.has("usermod -L") {
		t.Fatal("reconcile enforced an active account")
	}

	if err := f.st.SetUserStatus(ctx, f.user.ID, store.StatusSuspended); err != nil {
		t.Fatal(err)
	}
	f.s.Reconcile(ctx)
	if !f.cmds.has("usermod -L alice") {
		t.Fatal("reconcile did not enforce a newly suspended account")
	}
	f.cmds.reset()
	f.s.Reconcile(ctx) // second pass: nothing new to do
	if f.cmds.has("usermod -L") {
		t.Error("reconcile re-enforced an account it had already enforced")
	}

	if err := f.st.SetUserStatus(ctx, f.user.ID, store.StatusActive); err != nil {
		t.Fatal(err)
	}
	f.s.Reconcile(ctx)
	if !f.cmds.has("usermod -U alice") {
		t.Error("reconcile did not lift an account that is no longer suspended")
	}
}
