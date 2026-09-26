package api

import (
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"aegis/internal/store"
	"aegis/internal/svc"
)

// Import, export and multi-statement scripts for the database editor. These run
// longer than an interactive request, so they get their own time budget; they are
// still bound by the caller's own database grants, still owner-or-admin only,
// and audited.

const (
	editorLongTimeout = 6 * time.Hour // a multi-gigabyte import or export legitimately takes a while
)

var unsafeFilename = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// downloadName builds a safe Content-Disposition file name.
func downloadName(base, ext string) string {
	base = strings.Trim(unsafeFilename.ReplaceAllString(base, "_"), "._")
	if base == "" {
		base = "export"
	}
	return base + "-" + time.Now().UTC().Format("20060102-150405") + ext
}

// withEditorLong is withEditor with the long time budget and the connection's
// statement limit raised to match.
func (s *Server) withEditorLong(next editorHandler) http.HandlerFunc {
	return s.withEditorFor(editorLongTimeout, func(w http.ResponseWriter, r *http.Request, row *store.Database, c *svc.EditorConn) {
		c.AllowLongRun(r.Context(), editorLongTimeout)
		next(w, r, row, c)
	})
}

// handleEditorScript runs several statements in order. It answers 200 even when a
// statement fails, so the page can show what completed before the failure.
func (s *Server) handleEditorScript(w http.ResponseWriter, r *http.Request, row *store.Database, c *svc.EditorConn) {
	var req struct {
		SQL   string `json:"sql"`
		Limit int    `json:"limit"`
	}
	if !readJSONExact(w, r, &req) {
		return
	}
	results, err := c.ExecScript(r.Context(), req.SQL, req.Limit, func(stmt string) {
		s.editorAudit(r, "dbeditor.query", row, stmt)
	})
	out := map[string]any{"results": results}
	if err != nil {
		if len(results) == 0 {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		out["error"] = err.Error()
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleEditorDefinition(w http.ResponseWriter, r *http.Request, row *store.Database, c *svc.EditorConn) {
	def, err := c.Definition(r.Context(), r.PathValue("table"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"sql": def})
}

func flag(r *http.Request, name string) bool {
	switch strings.ToLower(r.URL.Query().Get(name)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// handleEditorExport streams a SQL dump or a CSV file. Once the body has started
// an error cannot change the status line, so a failure aborts the connection:
// the download then fails instead of saving a file that looks complete but is not.
func (s *Server) handleEditorExport(w http.ResponseWriter, r *http.Request, row *store.Database, c *svc.EditorConn) {
	q := r.URL.Query()
	format := q.Get("format")
	tables := q["table"]
	if len(tables) > 500 {
		writeErr(w, http.StatusBadRequest, "too many tables selected")
		return
	}
	opts := svc.ExportOptions{Tables: tables, Structure: flag(r, "structure"), Data: flag(r, "data"), DropIfExists: flag(r, "drop")}
	switch format {
	case "csv":
		if len(tables) != 1 {
			writeErr(w, http.StatusBadRequest, "choose exactly one table for a CSV export")
			return
		}
		// Validate before the first byte so a bad table name is a proper 400.
		if _, err := c.Structure(r.Context(), tables[0]); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
	case "sql":
		if !opts.Structure && !opts.Data {
			writeErr(w, http.StatusBadRequest, "choose structure, data or both")
			return
		}
	default:
		writeErr(w, http.StatusBadRequest, "format must be sql or csv")
		return
	}
	s.editorAudit(r, "dbeditor.export", row, format+" "+strings.Join(tables, ","))
	ext, ctype, base := ".sql", "application/sql; charset=utf-8", row.Name
	if format == "csv" {
		ext, ctype, base = ".csv", "text/csv; charset=utf-8", tables[0]
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", `attachment; filename="`+downloadName(base, ext)+`"`)

	var err error
	if format == "csv" {
		err = c.ExportCSV(r.Context(), w, tables[0], q.Get("header") != "0", q.Get("null"))
	} else {
		err = c.ExportSQL(r.Context(), w, row.Name, opts)
	}
	if err != nil {
		panic(http.ErrAbortHandler)
	}
}

// openUpload returns the "file" part of a multipart import as a stream, plus the
// small form fields that came before it (the page sends the file last). The file
// is never buffered: a large dump costs the panel no memory or disk.
func openUpload(w http.ResponseWriter, r *http.Request) (map[string]string, io.Reader, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, svc.EditorMaxImportBytes+(1<<20))
	mr, err := r.MultipartReader()
	if err != nil {
		writeErr(w, http.StatusBadRequest, "expected a file upload")
		return nil, nil, false
	}
	fields := map[string]string{}
	for {
		part, err := mr.NextPart()
		if err != nil {
			writeErr(w, http.StatusBadRequest, "choose a file to import")
			return nil, nil, false
		}
		if part.FormName() == "file" {
			return fields, part, true
		}
		v, _ := io.ReadAll(io.LimitReader(part, 4096))
		fields[part.FormName()] = string(v)
	}
}

func (s *Server) handleEditorImport(w http.ResponseWriter, r *http.Request, row *store.Database, c *svc.EditorConn) {
	fields, file, ok := openUpload(w, r)
	if !ok {
		return
	}
	var res *svc.ImportResult
	var err error
	switch fields["format"] {
	case "sql":
		s.editorAudit(r, "dbeditor.import", row, "sql (streamed)")
		res, err = c.ImportSQL(r.Context(), file)
	case "csv":
		table := fields["table"]
		opts := svc.CSVImportOptions{Header: fields["header"] != "0", NullToken: fields["null"]}
		switch d := fields["delimiter"]; d {
		case "":
		case "tab":
			opts.Delimiter = '\t'
		case ",", ";", "|":
			opts.Delimiter = rune(d[0])
		default:
			writeErr(w, http.StatusBadRequest, "delimiter must be a comma, semicolon, pipe or tab")
			return
		}
		s.editorAudit(r, "dbeditor.import", row, "csv into "+table+" (streamed)")
		res, err = c.ImportCSV(r.Context(), file, table, opts)
	default:
		writeErr(w, http.StatusBadRequest, "format must be sql or csv")
		return
	}
	if res != nil {
		s.editorAudit(r, "dbeditor.import.done", row, fmt.Sprintf("%d statements, %d rows, ok=%t", res.Statements, res.Rows, err == nil))
	}
	if err != nil {
		// 422 with the structured result, so the page can say which statement or row failed.
		if res == nil {
			res = &svc.ImportResult{}
		}
		if res.Error == "" {
			res.Error = err.Error()
		}
		writeJSON(w, http.StatusUnprocessableEntity, res)
		return
	}
	writeJSON(w, http.StatusOK, res)
}
