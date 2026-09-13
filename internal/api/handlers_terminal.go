package api

import (
	"net/http"

	"github.com/coder/websocket"
)

// handleTerminalWS upgrades to a WebSocket and hands the session to the
// terminal service. Package feature gate: users whose package disallows the
// terminal get a 403. (The route is also gated by withFeature; this check
// remains because WebSocket handshakes may want their own error framing.)
func (s *Server) handleTerminalWS(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	if !s.packageAllows(u, s.featuresFor(r.Context(), u)[FeatureTerminal]) {
		writeErr(w, http.StatusForbidden, "your package does not allow terminal access")
		return
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	_ = s.Terminal.Serve(r.Context(), u, conn)
}

