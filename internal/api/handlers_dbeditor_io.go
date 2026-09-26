package api

import (
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"aegis/internal/store"
	"aegis/internal/svc"
)

// Import, export and multi-statement scripts for the database editor. These run
// longer than an interactive request, so they get their own time budget; they are
// still bound by the caller's own database grants, still owner-or-admin only,
// and audited.

const (
	editorLongTimeout = 15 * time.Minute
	editorMaxUpload   = 64 << 20 // bytes accepted by one import
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

// uploadedFile reads the "file" part of a multipart import into memory, bounded.
func uploadedFile(w http.ResponseWriter, r *http.Request) (string, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, editorMaxUpload+(1<<20))
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		writeErr(w, http.StatusBadRequest, "the upload is invalid or larger than 64 MiB")
		return "", false
	}
	f, _, err := r.FormFile("file")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "choose a file to import")
		return "", false
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, editorMaxUpload+1))
	if err != nil || int64(len(data)) > editorMaxUpload {
		writeErr(w, http.StatusBadRequest, "the file is larger than 64 MiB")
		return "", false
	}
	text := strings.TrimPrefix(string(data), "\xef\xbb\xbf")
	if !utf8.ValidString(text) {
		writeErr(w, http.StatusBadRequest, "the file is not valid UTF-8 text")
		return "", false
	}
	return text, true
}

func (s *Server) handleEditorImport(w http.ResponseWriter, r *http.Request, row *store.Database, c *svc.EditorConn) {
	text, ok := uploadedFile(w, r)
	if !ok {
		return
	}
	defer r.MultipartForm.RemoveAll()
	format := r.FormValue("format")
	var res *svc.ImportResult
	var err error
	switch format {
	case "sql":
		s.editorAudit(r, "dbeditor.import", row, fmt.Sprintf("sql %d bytes", len(text)))
		res, err = c.ImportSQL(r.Context(), text)
	case "csv":
		table := r.FormValue("table")
		opts := svc.CSVImportOptions{Header: r.FormValue("header") != "0", NullToken: r.FormValue("null")}
		if d := r.FormValue("delimiter"); d != "" {
			switch d {
			case "tab":
				opts.Delimiter = '\t'
			case ",", ";", "|":
				opts.Delimiter = rune(d[0])
			default:
				writeErr(w, http.StatusBadRequest, "delimiter must be a comma, semicolon, pipe or tab")
				return
			}
		}
		s.editorAudit(r, "dbeditor.import", row, fmt.Sprintf("csv into %s, %d bytes", table, len(text)))
		res, err = c.ImportCSV(r.Context(), strings.NewReader(text), table, opts)
	default:
		writeErr(w, http.StatusBadRequest, "format must be sql or csv")
		return
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
