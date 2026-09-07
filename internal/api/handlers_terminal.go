package api

import (
	"context"
	"net/http"

	"github.com/coder/websocket"

	"aegis/internal/store"
)

// handleTerminalWS upgrades to a WebSocket and hands the session to the
// terminal service. Package feature gate: users whose package disallows the
// terminal get a 403.
func (s *Server) handleTerminalWS(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	if !s.pkgAllowsTerminal(u) {
		writeErr(w, http.StatusForbidden, "your package does not allow terminal access")
		return
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	_ = s.Terminal.Serve(r.Context(), u, conn)
}

func (s *Server) pkgAllowsTerminal(u *store.User) bool {
	if u.Role == store.RoleAdmin || u.Role == store.RoleReseller {
		return true
	}
	pkg, err := s.Store.GetPackage(context.Background(), u.PackageID)
	if err != nil {
		return true
	}
	return pkg.AllowTerminal
}
