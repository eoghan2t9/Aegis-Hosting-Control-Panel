package svc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"
)

// webServerUnits are the systemd units of every web server the panel can run
// (see serviceNameFor). Exactly one of them, or the in-process "go" server, may
// own :80/:443; the rest must stay stopped and disabled.
var webServerUnits = []string{"nginx", "apache2", "caddy"}

// strayWebServerUnits returns the web-server units that must NOT be running
// when active is the configured server. The native "go" server has no unit, so
// with it selected every unit is a stray. An unrecognised value returns nil:
// never shut anything down on the strength of a config we cannot interpret.
func strayWebServerUnits(active string) []string {
	switch active {
	case "go", "nginx", "apache", "caddy":
	default:
		return nil
	}
	keep := serviceNameFor(active)
	var out []string
	for _, u := range webServerUnits {
		if u != keep {
			out = append(out, u)
		}
	}
	return out
}

// webServerEnforceEnabled reports whether stray web servers are shut down;
// AEGIS_WEBSERVER_ENFORCE=off turns it off (e.g. while migrating by hand).
func webServerEnforceEnabled() bool {
	return strings.ToLower(strings.TrimSpace(os.Getenv("AEGIS_WEBSERVER_ENFORCE"))) != "off"
}

// lastLine returns the last non-empty line of s, trimmed.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

func systemctlCmd(args ...string) (string, error) {
	return RunTimeout(20*time.Second, "systemctl", args...)
}

// BeginSwitch serialises a web-server switch against EnforceSingle. A switch
// starts the new server before it persists the new setting, so without this the
// watchdog could see the old setting and shut the new server down mid-switch.
// Call the returned func (typically deferred) when the switch is finished.
func (w *WebServer) BeginSwitch() (done func()) {
	w.switchMu.Lock()
	return w.switchMu.Unlock
}

// EnforceSingle stops and disables every web-server unit other than the one
// selected in the panel (Cfg.WebServer.Server), so nothing else can hold
// :80/:443. A leftover enabled nginx grabbing :80 at boot, before aegis, took
// the panel and every site down in a crash loop. It is idempotent, does
// nothing on a non-systemd host, and returns the units it had to act on.
func (w *WebServer) EnforceSingle() ([]string, error) {
	if !webServerEnforceEnabled() {
		return nil, nil
	}
	ctl := w.unitCtl
	if ctl == nil {
		if !systemdIsInit() {
			return nil, nil
		}
		ctl = systemctlCmd
	}

	w.switchMu.Lock()
	defer w.switchMu.Unlock()

	var acted []string
	var errs []error
	for _, unit := range strayWebServerUnits(w.Cfg.WebServer.Server) {
		changed := false
		// is-active exits non-zero for anything but "active"; the printed
		// state is what matters, so the error is deliberately ignored.
		state, _ := ctl("is-active", unit)
		switch lastLine(state) {
		case "active", "activating", "reloading":
			changed = true
			if _, err := ctl("stop", unit); err != nil {
				errs = append(errs, fmt.Errorf("stop %s: %w", unit, err))
			}
		}
		// The state is the last line: systemctl can print a SysV-sync notice
		// (stderr is merged into the output) ahead of it for legacy units.
		if state, _ := ctl("is-enabled", unit); lastLine(state) == "enabled" {
			changed = true
			if _, err := ctl("disable", unit); err != nil {
				errs = append(errs, fmt.Errorf("disable %s: %w", unit, err))
			}
		}
		if changed {
			acted = append(acted, unit)
		}
	}
	return acted, errors.Join(errs...)
}

// WatchStrays runs EnforceSingle every interval until ctx is cancelled, so a
// package upgrade or a manual `systemctl enable` cannot bring a second web
// server back. Each pass is a few cheap systemctl queries.
func (w *WebServer) WatchStrays(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			w.ReportStrays(w.EnforceSingle())
		}
	}
}

// ReportStrays logs what EnforceSingle did; shared by the boot call and the
// watchdog so both word it the same way.
func (w *WebServer) ReportStrays(acted []string, err error) {
	if len(acted) > 0 {
		slog.Warn("stopped web server(s) not selected in the panel",
			"units", acted, "selected", w.Cfg.WebServer.Server)
	}
	if err != nil {
		slog.Warn("could not fully stop web server(s) not selected in the panel", "err", err)
	}
}
