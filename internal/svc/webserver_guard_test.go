package svc

import (
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"aegis/internal/config"
)

// fakeUnits is a stand-in for systemctl: it tracks each unit's state and
// records every mutating call, so a test can assert exactly what was touched.
type fakeUnits struct {
	mu      sync.Mutex
	active  map[string]bool
	enabled map[string]bool
	calls   []string
	stopErr error
}

func newFakeUnits() *fakeUnits {
	return &fakeUnits{active: map[string]bool{}, enabled: map[string]bool{}}
}

func (f *fakeUnits) ctl(args ...string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	verb, unit := args[0], args[1]
	switch verb {
	case "is-active":
		if f.active[unit] {
			return "active", nil
		}
		return "inactive", errors.New("exit status 3")
	case "is-enabled":
		if f.enabled[unit] {
			return "enabled", nil
		}
		return "disabled", errors.New("exit status 1")
	case "stop":
		f.calls = append(f.calls, "stop "+unit)
		if f.stopErr != nil {
			return "", f.stopErr
		}
		f.active[unit] = false
	case "disable":
		f.calls = append(f.calls, "disable "+unit)
		f.enabled[unit] = false
	}
	return "", nil
}

func guardFor(server string, f *fakeUnits) *WebServer {
	return &WebServer{Cfg: &config.Config{WebServer: config.WebServerConfig{Server: server}}, unitCtl: f.ctl}
}

func TestStrayWebServerUnits(t *testing.T) {
	cases := map[string][]string{
		"go":     {"nginx", "apache2", "caddy"},
		"nginx":  {"apache2", "caddy"},
		"apache": {"nginx", "caddy"},
		"caddy":  {"nginx", "apache2"},
		"":       nil,
		"bogus":  nil,
		"NGINX ": nil,
	}
	for active, want := range cases {
		if got := strayWebServerUnits(active); !reflect.DeepEqual(got, want) {
			t.Errorf("strayWebServerUnits(%q) = %v, want %v", active, got, want)
		}
	}
}

func TestLastLine(t *testing.T) {
	cases := map[string]string{
		"enabled":    "enabled",
		"  active\n": "active",
		"Synchronizing state of nginx.service with SysV service script\nExecuting: /usr/lib/systemd/systemd-sysv-install is-enabled nginx\nenabled": "enabled",
		"": "",
	}
	for in, want := range cases {
		if got := lastLine(in); got != want {
			t.Errorf("lastLine(%q) = %q, want %q", in, got, want)
		}
	}
}

// The outage this guards against: config says "go", but nginx is enabled and
// running and would take :80 before aegis can.
func TestEnforceSingleStopsAndDisablesStrays(t *testing.T) {
	f := newFakeUnits()
	f.active["nginx"], f.enabled["nginx"] = true, true
	w := guardFor("go", f)

	acted, err := w.EnforceSingle()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(acted, []string{"nginx"}) {
		t.Errorf("acted = %v, want [nginx]", acted)
	}
	if got := strings.Join(f.calls, ","); got != "stop nginx,disable nginx" {
		t.Errorf("calls = %q", got)
	}

	// Idempotent: a second pass finds nothing to do and touches nothing.
	f.calls = nil
	if acted, err := w.EnforceSingle(); err != nil || len(acted) != 0 || len(f.calls) != 0 {
		t.Errorf("second pass acted=%v err=%v calls=%v", acted, err, f.calls)
	}
}

func TestEnforceSingleLeavesSelectedServerAlone(t *testing.T) {
	f := newFakeUnits()
	f.active["nginx"], f.enabled["nginx"] = true, true
	f.active["apache2"] = true // running but not enabled: still a stray
	w := guardFor("nginx", f)

	acted, err := w.EnforceSingle()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(acted, []string{"apache2"}) {
		t.Errorf("acted = %v, want [apache2]", acted)
	}
	if !f.active["nginx"] || !f.enabled["nginx"] {
		t.Error("the selected server was touched")
	}
	if got := strings.Join(f.calls, ","); got != "stop apache2" {
		t.Errorf("calls = %q", got)
	}
}

func TestEnforceSingleDisablesEnabledButStopped(t *testing.T) {
	f := newFakeUnits()
	f.enabled["caddy"] = true // not running, but would start at next boot
	w := guardFor("go", f)
	acted, _ := w.EnforceSingle()
	if !reflect.DeepEqual(acted, []string{"caddy"}) || strings.Join(f.calls, ",") != "disable caddy" {
		t.Errorf("acted=%v calls=%v", acted, f.calls)
	}
}

func TestEnforceSingleIgnoresUnknownConfig(t *testing.T) {
	f := newFakeUnits()
	f.active["nginx"], f.enabled["nginx"] = true, true
	for _, server := range []string{"", "bogus"} {
		if acted, err := guardFor(server, f).EnforceSingle(); err != nil || len(acted) != 0 {
			t.Errorf("server %q: acted=%v err=%v", server, acted, err)
		}
	}
	if len(f.calls) != 0 {
		t.Errorf("touched units on an unreadable config: %v", f.calls)
	}
}

func TestEnforceSingleCanBeDisabled(t *testing.T) {
	t.Setenv("AEGIS_WEBSERVER_ENFORCE", "off")
	f := newFakeUnits()
	f.active["nginx"], f.enabled["nginx"] = true, true
	if acted, err := guardFor("go", f).EnforceSingle(); err != nil || len(acted) != 0 || len(f.calls) != 0 {
		t.Errorf("acted=%v err=%v calls=%v", acted, err, f.calls)
	}
}

func TestEnforceSingleReportsFailedStop(t *testing.T) {
	f := newFakeUnits()
	f.active["nginx"] = true
	f.stopErr = errors.New("boom")
	acted, err := guardFor("go", f).EnforceSingle()
	if err == nil || !strings.Contains(err.Error(), "stop nginx") {
		t.Errorf("err = %v, want a stop nginx failure", err)
	}
	if !reflect.DeepEqual(acted, []string{"nginx"}) {
		t.Errorf("acted = %v", acted)
	}
}

// A switch starts the new server before saving the new setting; the watchdog
// must not run in that window or it would shut the new server down.
func TestBeginSwitchBlocksEnforce(t *testing.T) {
	f := newFakeUnits()
	f.active["nginx"] = true
	w := guardFor("go", f)

	done := w.BeginSwitch()
	finished := make(chan struct{})
	go func() {
		_, _ = w.EnforceSingle()
		close(finished)
	}()
	select {
	case <-finished:
		t.Fatal("EnforceSingle ran while a switch was in progress")
	case <-time.After(50 * time.Millisecond):
	}
	// The switch commits the new setting, then releases.
	w.Cfg.WebServer.Server = "nginx"
	done()
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("EnforceSingle never resumed after the switch finished")
	}
	if !f.active["nginx"] {
		t.Error("the newly selected server was stopped by the watchdog")
	}
}
