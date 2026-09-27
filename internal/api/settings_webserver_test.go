package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"aegis/internal/config"
	"aegis/internal/svc"
)

// Saving settings must not be a back door for changing the web server: it only
// rewrites config, so the config would then disagree with what is bound to
// :80/:443. Switching goes through PATCH /api/webserver.
func TestSettingsPutCannotChangeWebServer(t *testing.T) {
	cfg := &config.Config{WebServer: config.WebServerConfig{Server: "go"}}
	s := &Server{Cfg: cfg, Web: svc.NewWebServer(cfg, nil)}

	req := httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(`{"web_server":"nginx"}`))
	rec := httptest.NewRecorder()
	s.handleSettingsPut(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Runtime page") {
		t.Errorf("error does not point at the switch: %s", rec.Body.String())
	}
	if cfg.WebServer.Server != "go" {
		t.Errorf("config changed to %q despite the rejection", cfg.WebServer.Server)
	}
}

func TestApplyToConfigNeverTouchesWebServer(t *testing.T) {
	cfg := &config.Config{WebServer: config.WebServerConfig{Server: "go"}}
	v := settingsFromConfig(cfg)
	v.WebServer = "nginx"
	v.applyToConfig(cfg)
	if cfg.WebServer.Server != "go" {
		t.Errorf("applyToConfig changed the active web server to %q", cfg.WebServer.Server)
	}
}
