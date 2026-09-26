package svc

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	osuser "os/user"
	"strconv"
	"syscall"

	"github.com/coder/websocket"
	"github.com/creack/pty"

	"aegis/internal/config"
	"aegis/internal/store"
)

// Terminal runs an interactive shell for a user inside their home directory
// over a WebSocket. Used by the built-in file/SSH terminal (feature 10).
type Terminal struct {
	Cfg *config.Config
}

func NewTerminal(cfg *config.Config) *Terminal { return &Terminal{Cfg: cfg} }

// terminalEnv is the complete environment a customer's shell receives. It is
// built from scratch on purpose: the panel process's own environment carries
// the database admin passwords (from aegis.env) and must never reach a shell
// a customer can type into.
func terminalEnv(name, home string) []string {
	return []string{
		"TERM=xterm-256color",
		"HOME=" + home,
		"USER=" + name,
		"LOGNAME=" + name,
		"SHELL=/bin/bash",
		"PWD=" + home,
		"LANG=C.UTF-8",
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
	}
}

// terminalCommand builds the shell for an account so that it runs AS that
// account. The panel itself runs as root, and a child inherits its parent's
// credentials unless told otherwise: without the Credential below every
// customer who opened the terminal got a root shell.
func terminalCommand(name, home string, uid, gid uint32, groups []uint32) (*exec.Cmd, error) {
	if uid == 0 || gid == 0 {
		return nil, errors.New("refusing to start a terminal as root")
	}
	cmd := exec.Command("/bin/bash", "-l")
	cmd.Dir = home
	cmd.Env = terminalEnv(name, home)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid:     true,
		Setctty:    true,
		Credential: &syscall.Credential{Uid: uid, Gid: gid, Groups: groups},
	}
	return cmd, nil
}

// terminalIdentity resolves a panel user's system account to numeric ids.
func terminalIdentity(name string) (uid, gid uint32, groups []uint32, err error) {
	u, err := osuser.Lookup(name)
	if err != nil {
		return 0, 0, nil, err
	}
	u64, err := strconv.ParseUint(u.Uid, 10, 32)
	if err != nil {
		return 0, 0, nil, err
	}
	g64, err := strconv.ParseUint(u.Gid, 10, 32)
	if err != nil {
		return 0, 0, nil, err
	}
	if ids, gerr := u.GroupIds(); gerr == nil {
		for _, s := range ids {
			if n, perr := strconv.ParseUint(s, 10, 32); perr == nil {
				groups = append(groups, uint32(n))
			}
		}
	}
	return uint32(u64), uint32(g64), groups, nil
}

// termMsg is the client→server protocol: either raw input or a resize.
type termMsg struct {
	Type string `json:"type,omitempty"` // "input" (default) | "resize"
	Data string `json:"data,omitempty"`
	Cols int    `json:"cols,omitempty"`
	Rows int    `json:"rows,omitempty"`
}

// Serve drives a terminal session for the lifetime of the websocket.
func (t *Terminal) Serve(ctx context.Context, user *store.User, conn *websocket.Conn) error {
	home := user.HomeDir
	if home == "" {
		home = t.Cfg.HomeRoot + "/" + user.Username
	}
	uid, gid, groups, err := terminalIdentity(user.Username)
	if err != nil {
		return errors.New("terminal: this account has no system user")
	}
	cmd, err := terminalCommand(user.Username, home, uid, gid, groups)
	if err != nil {
		return err
	}
	ptmx, tty, err := pty.Open()
	if err != nil {
		return errors.New("could not allocate a terminal: " + err.Error())
	}
	// The slave end is created by the panel (root); hand it to the user so
	// tty-aware programs (tmux, screen, write) can use it.
	_ = os.Chown(tty.Name(), int(uid), int(gid))
	_ = os.Chmod(tty.Name(), 0o620)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = tty, tty, tty
	if err := cmd.Start(); err != nil {
		_ = ptmx.Close()
		_ = tty.Close()
		return errors.New("could not start shell: " + err.Error())
	}
	_ = tty.Close()
	defer func() {
		_ = ptmx.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	// Pump PTY output → websocket.
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				_ = conn.Write(ctx, websocket.MessageText, buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()

	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return nil // client disconnected
		}
		var msg termMsg
		if err := json.Unmarshal(data, &msg); err == nil && msg.Type == "resize" {
			if msg.Cols > 0 && msg.Rows > 0 {
				_ = pty.Setsize(ptmx, &pty.Winsize{Rows: uint16(msg.Rows), Cols: uint16(msg.Cols)})
			}
			continue
		}
		payload := string(data)
		if msg.Type == "resize" {
			continue
		}
		if msg.Type == "input" {
			payload = msg.Data
		}
		if payload != "" {
			if _, err := ptmx.Write([]byte(payload)); err != nil {
				return nil
			}
		}
	}
}
