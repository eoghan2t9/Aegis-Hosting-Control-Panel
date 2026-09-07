package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/coder/websocket"

	"aegis/internal/store"
	"aegis/internal/svc"
)

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	ov := s.System.Overview()
	writeJSON(w, http.StatusOK, ov)
}

// handleUsage reports per-user resource usage. Admins see all users; regular
// users see only their own account.
func (s *Server) handleUsage(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	usernames := map[int]string{}
	if u.Role == store.RoleAdmin {
		users, _ := s.Store.ListUsers(r.Context(), 0)
		for _, uu := range users {
			if uid, err := svcUID(uu.Username); err == nil {
				usernames[uid] = uu.Username
			}
		}
	} else {
		if uid, err := svcUID(u.Username); err == nil {
			usernames[uid] = u.Username
		}
	}
	usage, err := s.System.PerUserUsage(usernames)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, usage)
}

func (s *Server) handleProcesses(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	uidFilter := 0
	if u.Role != store.RoleAdmin {
		if uid, err := svcUID(u.Username); err == nil {
			uidFilter = uid
		}
	}
	procs, err := s.System.Processes(uidFilter)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, procs)
}

// handleMetricsWS streams realtime resource snapshots to the dashboard.
func (s *Server) handleMetricsWS(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	ctx := r.Context()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m := s.System.MetricsSnapshot()
			if err := writeWSJSON(ctx, conn, m); err != nil {
				return
			}
		}
	}
}

func writeWSJSON(ctx context.Context, conn *websocket.Conn, v interface{}) error {
	ctx2, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return conn.Write(ctx2, websocket.MessageText, mustJSON(v))
}

func mustJSON(v interface{}) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte("{}")
	}
	return b
}

func svcUID(username string) (int, error) {
	return svc.UIDFor(username)
}

// --- tuning ----------------------------------------------------------------------

func (s *Server) tuningSummary() interface{} {
	report, err := s.Tuner.LoadReport()
	if err != nil {
		return map[string]interface{}{"tuned": false}
	}
	return map[string]interface{}{"tuned": true, "report": report}
}

func (s *Server) handleTuningReport(w http.ResponseWriter, r *http.Request) {
	report, err := s.Tuner.LoadReport()
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"tuned": false})
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (s *Server) handleTuningInspect(w http.ResponseWriter, r *http.Request) {
	ins := s.Tuner.Inspect()
	s.audit(r, "tuning.inspect", "", "")
	writeJSON(w, http.StatusOK, ins)
}

func (s *Server) handleTuningApply(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ApplySysctl bool `json:"apply_sysctl"`
	}
	_ = readJSON(w, r, &req)
	report, err := s.Tuner.Tune()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if req.ApplySysctl {
		if err := s.Tuner.ApplySysctl(report); err != nil {
			writeErr(w, http.StatusBadGateway, "sysctl apply: "+err.Error())
			return
		}
	}
	s.audit(r, "tuning.apply", "", "cores="+strconv.Itoa(report.Cores))
	writeJSON(w, http.StatusOK, report)
}
