package svc

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"

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
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/bash"
	}
	cmd := exec.Command(shell, "-l")
	cmd.Dir = home
	cmd.Env = append(os.Environ(),
		"TERM=xterm-256color",
		"HOME="+home,
		"USER="+user.Username,
		"LOGNAME="+user.Username,
		"PWD="+home,
	)
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return errors.New("could not start shell: " + err.Error())
	}
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
