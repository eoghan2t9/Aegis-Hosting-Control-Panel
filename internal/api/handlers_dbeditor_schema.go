package api

import (
	"net/http"
	"strings"

	"aegis/internal/store"
	"aegis/internal/svc"
)

// Schema editing, maintenance, search and object listing for the database
// editor. The client sends structured requests, never DDL: svc generates the
// statements from allow-listed types and validated names (see dbeditor_schema.go).

// handleEditorInfo tells the page which engine it is talking to and which
// column types it may offer.
func (s *Server) handleEditorInfo(w http.ResponseWriter, r *http.Request, row *store.Database, c *svc.EditorConn) {
	writeJSON(w, http.StatusOK, map[string]any{
		"engine": row.Server,
		"name":   row.Name,
		"types":  svc.SchemaTypes(row.Server),
	})
}

// handleEditorSchema previews or applies one schema change. Statements are
// audited one by one just before they run.
func (s *Server) handleEditorSchema(w http.ResponseWriter, r *http.Request, row *store.Database, c *svc.EditorConn) {
	var req struct {
		svc.SchemaRequest
		Preview bool   `json:"preview"`
		Confirm string `json:"confirm"`
	}
	if !readJSONExact(w, r, &req) {
		return
	}
	// Dropping a column destroys its data: like dropping a table, it must be
	// confirmed by name, so a stray or replayed request cannot do it.
	if !req.Preview && req.Action == "drop_column" && req.Confirm != req.Name {
		writeErr(w, http.StatusBadRequest, "type the column name to confirm")
		return
	}
	res, err := c.ApplySchema(r.Context(), req.SchemaRequest, req.Preview, func(stmt string) {
		s.editorAudit(r, "dbeditor.schema", row, stmt)
	})
	if err != nil {
		if res == nil { // rejected while planning: nothing ran
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusUnprocessableEntity, res)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleEditorMaintenance(w http.ResponseWriter, r *http.Request, row *store.Database, c *svc.EditorConn) {
	var req struct {
		Op string `json:"op"`
	}
	if !readJSONExact(w, r, &req) {
		return
	}
	table := r.PathValue("table")
	s.editorAudit(r, "dbeditor.maintenance", row, req.Op+" "+table)
	res, stmt, err := c.Maintenance(r.Context(), table, req.Op)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"statement": stmt, "result": res})
}

func (s *Server) handleEditorSearch(w http.ResponseWriter, r *http.Request, row *store.Database, c *svc.EditorConn) {
	q := r.URL.Query()
	tables := q["table"]
	if len(tables) > 500 {
		writeErr(w, http.StatusBadRequest, "too many tables selected")
		return
	}
	s.editorAudit(r, "dbeditor.search", row, strings.TrimSpace(q.Get("q")))
	res, err := c.Search(r.Context(), q.Get("q"), tables)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleEditorObjects(w http.ResponseWriter, r *http.Request, row *store.Database, c *svc.EditorConn) {
	objs, err := c.Objects(r.Context())
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, objs)
}
