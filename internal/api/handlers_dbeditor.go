package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"aegis/internal/store"
	"aegis/internal/svc"
)

// The database editor (a phpMyAdmin-style browser/SQL console). Every request
// connects to the database as its own user, so the database server's grants
// are the security boundary; see svc.DBEditor. Only the database's owner (or an
// admin) can use it, and every write and every SQL statement is audit-logged.

// decodeExact decodes JSON keeping numbers as exact text (json.Number). A plain
// decode turns every number into a float64, which silently rounds integers
// above 2^53 — a BIGINT value or key would be written back changed.
func decodeExact(rd io.Reader, v any) error {
	dec := json.NewDecoder(rd)
	dec.UseNumber()
	return dec.Decode(v)
}

// readJSONExact is readJSON for the editor: exact numbers, and room for large
// text values (a cell can hold far more than the panel's usual 1 MiB body).
func readJSONExact(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := decodeExact(http.MaxBytesReader(w, r.Body, 8<<20), v); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return false
	}
	return true
}

type editorHandler func(w http.ResponseWriter, r *http.Request, row *store.Database, c *svc.EditorConn)

// withEditor authorises the caller for the database in the URL, opens a
// connection as that database's own user and closes it afterwards.
func (s *Server) withEditor(next editorHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.Editor == nil {
			writeErr(w, http.StatusServiceUnavailable, "the database editor is not available")
			return
		}
		id, err := pathID(r, "id")
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		row, err := s.Store.GetDatabase(r.Context(), id)
		if err != nil {
			writeErr(w, http.StatusNotFound, "database not found")
			return
		}
		u := userFrom(r)
		if row.UserID != u.ID && u.Role != store.RoleAdmin {
			writeErr(w, http.StatusForbidden, "cannot manage this database")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
		defer cancel()
		conn, err := s.Editor.Open(ctx, row)
		if err != nil {
			writeErr(w, http.StatusBadGateway, err.Error())
			return
		}
		defer conn.Close()
		w.Header().Set("Cache-Control", "no-store") // results can hold anything in the database
		next(w, r.WithContext(ctx), row, conn)
	}
}

// editorAudit records a write or SQL statement, on a single line and capped.
func (s *Server) editorAudit(r *http.Request, action string, row *store.Database, detail string) {
	detail = strings.Join(strings.Fields(detail), " ")
	if len(detail) > 300 {
		detail = detail[:300] + "…"
	}
	s.audit(r, action, row.Server+"/"+row.Name, detail)
}

func (s *Server) handleEditorTables(w http.ResponseWriter, r *http.Request, row *store.Database, c *svc.EditorConn) {
	tables, err := c.Tables(r.Context())
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, tables)
}

func (s *Server) handleEditorStructure(w http.ResponseWriter, r *http.Request, row *store.Database, c *svc.EditorConn) {
	st, err := c.Structure(r.Context(), r.PathValue("table"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleEditorRows(w http.ResponseWriter, r *http.Request, row *store.Database, c *svc.EditorConn) {
	q := r.URL.Query()
	req := svc.EditorBrowse{Table: r.PathValue("table"), Sort: q.Get("sort"), Desc: strings.EqualFold(q.Get("dir"), "desc")}
	req.Page, _ = strconv.Atoi(q.Get("page"))
	req.PageSize, _ = strconv.Atoi(q.Get("page_size"))
	if f := q.Get("filters"); f != "" {
		if err := json.Unmarshal([]byte(f), &req.Filters); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid filters")
			return
		}
	}
	res, err := c.Browse(r.Context(), req)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

type editorRowReq struct {
	Key    map[string]any `json:"key"`
	Values map[string]any `json:"values"`
}

func (s *Server) handleEditorRowGet(w http.ResponseWriter, r *http.Request, row *store.Database, c *svc.EditorConn) {
	var key map[string]any
	if err := decodeExact(strings.NewReader(r.URL.Query().Get("key")), &key); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid key")
		return
	}
	out, err := c.GetRow(r.Context(), r.PathValue("table"), key)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleEditorRowInsert(w http.ResponseWriter, r *http.Request, row *store.Database, c *svc.EditorConn) {
	var req editorRowReq
	if !readJSONExact(w, r, &req) {
		return
	}
	n, err := c.Insert(r.Context(), r.PathValue("table"), req.Values)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.editorAudit(r, "dbeditor.insert", row, r.PathValue("table"))
	writeJSON(w, http.StatusOK, map[string]int64{"affected": n})
}

func (s *Server) handleEditorRowUpdate(w http.ResponseWriter, r *http.Request, row *store.Database, c *svc.EditorConn) {
	var req editorRowReq
	if !readJSONExact(w, r, &req) {
		return
	}
	n, err := c.Update(r.Context(), r.PathValue("table"), req.Key, req.Values)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.editorAudit(r, "dbeditor.update", row, r.PathValue("table"))
	writeJSON(w, http.StatusOK, map[string]int64{"affected": n})
}

func (s *Server) handleEditorRowDelete(w http.ResponseWriter, r *http.Request, row *store.Database, c *svc.EditorConn) {
	var req editorRowReq
	if !readJSONExact(w, r, &req) {
		return
	}
	n, err := c.Delete(r.Context(), r.PathValue("table"), req.Key)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.editorAudit(r, "dbeditor.delete", row, r.PathValue("table"))
	writeJSON(w, http.StatusOK, map[string]int64{"affected": n})
}

func (s *Server) handleEditorQuery(w http.ResponseWriter, r *http.Request, row *store.Database, c *svc.EditorConn) {
	var req struct {
		SQL   string `json:"sql"`
		Limit int    `json:"limit"`
	}
	if !readJSONExact(w, r, &req) {
		return
	}
	// Audit before running: a statement that fails or hangs is still a statement
	// someone tried to run on this database.
	s.editorAudit(r, "dbeditor.query", row, req.SQL)
	res, err := c.Exec(r.Context(), req.SQL, req.Limit)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// handleEditorDestroy serves both truncate and drop; the caller must repeat the
// table's name to confirm, so a stray request can never do it by accident.
func (s *Server) handleEditorDestroy(drop bool) editorHandler {
	return func(w http.ResponseWriter, r *http.Request, row *store.Database, c *svc.EditorConn) {
		var req struct {
			Confirm string `json:"confirm"`
		}
		if !readJSONExact(w, r, &req) {
			return
		}
		table := r.PathValue("table")
		if req.Confirm != table {
			writeErr(w, http.StatusBadRequest, "type the table name to confirm")
			return
		}
		var err error
		action := "dbeditor.truncate"
		if drop {
			action = "dbeditor.drop"
			err = c.Drop(r.Context(), table)
		} else {
			err = c.Truncate(r.Context(), table)
		}
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		s.editorAudit(r, action, row, table)
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}
