package svc

import (
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Import, export and multi-statement scripts for the database editor. All of it
// is native Go over the same per-database connection as the rest of the editor
// (no external dump tools, no admin credentials).

const (
	editorMaxScriptStatements = 100     // statements one console run may contain
	editorMaxImportStatements = 500_000 // statements one SQL import may contain
	editorExportBatchRows     = 100     // rows per INSERT in an SQL export
	editorExportBatchBytes    = 512 << 10
	editorCSVBatchParams      = 60_000 // bind parameters per CSV-import INSERT (servers allow 65,535)
	editorExportMaxBytes      = 1 << 30
)

// ---------------------------------------------------------------- statement splitting

func hasPrefixFold(s, prefix string) bool {
	return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
}

func isIdentByte(b byte) bool {
	return b == '_' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

// splitStatements splits a SQL script into single statements the way a client
// (mysql, psql, phpMyAdmin) would: semicolons inside quotes, comments and
// PostgreSQL $$ bodies do not end a statement; MySQL's DELIMITER command is
// honoured; comments are dropped; empty statements are skipped.
func splitStatements(src, kind string) []string {
	mysqlKind := kind != "postgres"
	var out []string
	var cur strings.Builder
	delim := ";"
	n := len(src)
	i := 0
	flush := func() {
		if s := strings.TrimSpace(cur.String()); s != "" {
			out = append(out, s)
		}
		cur.Reset()
	}
	// copyQuoted copies a quoted run starting at i (src[i] is the quote) and
	// returns the index after the closing quote. Doubled quotes are an escaped
	// quote; in MySQL a backslash also escapes the next character.
	copyQuoted := func(q byte, backslash bool) int {
		cur.WriteByte(q)
		j := i + 1
		for j < n {
			ch := src[j]
			if backslash && ch == '\\' && j+1 < n {
				cur.WriteByte(ch)
				cur.WriteByte(src[j+1])
				j += 2
				continue
			}
			cur.WriteByte(ch)
			j++
			if ch == q {
				if j < n && src[j] == q {
					cur.WriteByte(q)
					j++
					continue
				}
				break
			}
		}
		return j
	}
	for i < n {
		c := src[i]
		lineStart := i == 0 || src[i-1] == '\n'
		switch {
		case mysqlKind && lineStart && strings.TrimSpace(cur.String()) == "" && hasPrefixFold(src[i:], "DELIMITER "):
			eol := strings.IndexByte(src[i:], '\n')
			line := src[i:]
			if eol >= 0 {
				line = src[i : i+eol]
			}
			if d := strings.TrimSpace(line[len("DELIMITER "):]); d != "" {
				delim = d
			}
			i += len(line)
		case c == '\'':
			i = copyQuoted('\'', mysqlKind)
		case c == '"':
			i = copyQuoted('"', mysqlKind)
		case c == '`' && mysqlKind:
			i = copyQuoted('`', false)
		case c == '-' && i+1 < n && src[i+1] == '-' && (!mysqlKind || i+2 >= n || src[i+2] == ' ' || src[i+2] == '\t' || src[i+2] == '\n' || src[i+2] == '\r'),
			c == '#' && mysqlKind:
			for i < n && src[i] != '\n' { // a line comment: dropped, its newline kept
				i++
			}
		case c == '/' && i+1 < n && src[i+1] == '*':
			end := strings.Index(src[i+2:], "*/")
			stop := n
			if end >= 0 {
				stop = i + 2 + end + 2
			}
			if mysqlKind && i+2 < n && src[i+2] == '!' { // /*! ... */ is executed by MySQL: keep it
				cur.WriteString(src[i:stop])
			} else {
				cur.WriteByte(' ')
			}
			i = stop
		case c == '$' && !mysqlKind:
			j := i + 1
			for j < n && isIdentByte(src[j]) {
				j++
			}
			if j < n && src[j] == '$' && (j == i+1 || !(src[i+1] >= '0' && src[i+1] <= '9')) {
				tag := src[i : j+1]
				end := strings.Index(src[j+1:], tag)
				stop := n
				if end >= 0 {
					stop = j + 1 + end + len(tag)
				}
				cur.WriteString(src[i:stop])
				i = stop
			} else {
				cur.WriteByte(c)
				i++
			}
		case strings.HasPrefix(src[i:], delim):
			flush()
			i += len(delim)
		default:
			cur.WriteByte(c)
			i++
		}
	}
	flush()
	return out
}

// ---------------------------------------------------------------- literals

func quoteSQLString(d dialect, s string) string {
	if d.kind == "postgres" {
		return "'" + strings.ReplaceAll(s, "'", "''") + "'"
	}
	r := strings.NewReplacer(`\`, `\\`, `'`, `''`, "\x00", `\0`, "\x1a", `\Z`)
	return "'" + r.Replace(s) + "'"
}

// sqlLiteral renders a scanned value as a SQL literal for an INSERT statement.
func sqlLiteral(d dialect, v any, dbType string) string {
	switch x := v.(type) {
	case nil:
		return "NULL"
	case bool:
		if d.kind == "postgres" {
			if x {
				return "TRUE"
			}
			return "FALSE"
		}
		if x {
			return "1"
		}
		return "0"
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64)
	case time.Time:
		if strings.EqualFold(dbType, "DATE") {
			return quoteSQLString(d, x.Format("2006-01-02"))
		}
		return quoteSQLString(d, x.Format("2006-01-02 15:04:05.999999999-07:00"))
	case []byte:
		if isBinaryType(dbType) || !utf8.Valid(x) {
			if d.kind == "postgres" {
				return "'\\x" + hex.EncodeToString(x) + "'::bytea"
			}
			return "X'" + hex.EncodeToString(x) + "'"
		}
		return quoteSQLString(d, string(x))
	case string:
		return quoteSQLString(d, x)
	default:
		return quoteSQLString(d, fmt.Sprint(x))
	}
}

// ---------------------------------------------------------------- definitions

func quoteList(d dialect, names []string) string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = d.quote(n)
	}
	return strings.Join(out, ", ")
}

// pgCreateStatements builds CREATE TABLE (and CREATE INDEX) statements for a
// PostgreSQL table from its structure. Serial columns are emitted as SERIAL so
// the script does not depend on a sequence that only exists in the source.
func pgCreateStatements(st *EditorStructure) []string {
	q := pgDialect.quote
	var lines []string
	for _, col := range st.Columns {
		typ := col.Type
		def := col.Default
		if col.Extra == "auto_increment" {
			switch strings.ToLower(typ) {
			case "smallint":
				typ = "SMALLSERIAL"
			case "bigint":
				typ = "BIGSERIAL"
			default:
				typ = "SERIAL"
			}
			def = nil
		}
		line := q(col.Name) + " " + typ
		if !col.Nullable && col.Extra != "auto_increment" {
			line += " NOT NULL"
		}
		if def != nil {
			line += " DEFAULT " + *def
		}
		lines = append(lines, line)
	}
	if len(st.PrimaryKey) > 0 {
		lines = append(lines, "PRIMARY KEY ("+quoteList(pgDialect, st.PrimaryKey)+")")
	}
	out := []string{"CREATE TABLE " + q(st.Table) + " (\n  " + strings.Join(lines, ",\n  ") + "\n)"}
	for _, ix := range st.Indexes {
		if ix.Primary {
			continue
		}
		stmt := "CREATE "
		if ix.Unique {
			stmt += "UNIQUE "
		}
		stmt += "INDEX " + q(ix.Name) + " ON " + q(st.Table)
		if ix.Method != "" && ix.Method != "btree" {
			stmt += " USING " + ix.Method
		}
		out = append(out, stmt+" ("+quoteList(pgDialect, ix.Columns)+")")
	}
	return out
}

func fkStatement(d dialect, table string, fk EditorForeignKey) string {
	return "ALTER TABLE " + d.quote(table) + " ADD CONSTRAINT " + d.quote(fk.Name) + " FOREIGN KEY (" + quoteList(d, fk.Columns) +
		") REFERENCES " + d.quote(fk.RefTable) + " (" + quoteList(d, fk.RefColumns) + ")"
}

// Definition returns the DDL for a table or view: MySQL's own SHOW CREATE, or
// statements reconstructed from the catalog on PostgreSQL.
func (c *EditorConn) Definition(ctx context.Context, table string) (string, error) {
	st, err := c.Structure(ctx, table)
	if err != nil {
		return "", err
	}
	ddl, err := c.definitionFor(ctx, st, true)
	if err != nil {
		return "", err
	}
	return strings.Join(ddl, ";\n") + ";", nil
}

// definitionFor returns the statements that recreate a table or view. On
// PostgreSQL foreign keys are included only when withFK is set (an export
// applies them after all the data is loaded); MySQL's SHOW CREATE always
// includes them, which is why an import switches FOREIGN_KEY_CHECKS off.
func (c *EditorConn) definitionFor(ctx context.Context, st *EditorStructure, withFK bool) ([]string, error) {
	if c.d.kind == "postgres" {
		if st.Type == "view" {
			var def string
			if err := c.db.QueryRowContext(ctx, "SELECT pg_get_viewdef($1::regclass, true)", c.d.quote(st.Table)).Scan(&def); err != nil {
				return nil, err
			}
			return []string{"CREATE VIEW " + c.d.quote(st.Table) + " AS\n" + strings.TrimSuffix(strings.TrimSpace(def), ";")}, nil
		}
		stmts := pgCreateStatements(st)
		if withFK {
			for _, fk := range st.ForeignKeys {
				stmts = append(stmts, fkStatement(c.d, st.Table, fk))
			}
		}
		return stmts, nil
	}
	kw := "TABLE"
	if st.Type == "view" {
		kw = "VIEW"
	}
	rows, err := c.db.QueryContext(ctx, "SHOW CREATE "+kw+" "+c.d.quote(st.Table))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, errors.New("no definition returned")
	}
	cols, _ := rows.Columns()
	vals := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	if err := rows.Scan(ptrs...); err != nil {
		return nil, err
	}
	if len(vals) >= 2 {
		switch v := vals[1].(type) {
		case []byte:
			return []string{string(v)}, nil
		case string:
			return []string{v}, nil
		}
	}
	return nil, errors.New("unexpected SHOW CREATE result")
}

// ---------------------------------------------------------------- long runs

// AllowLongRun raises this connection's statement limit for an import or export,
// which legitimately run longer than an interactive statement. The caller's
// context still bounds the whole operation.
func (c *EditorConn) AllowLongRun(ctx context.Context, d time.Duration) {
	if c.d.kind == "postgres" {
		_, _ = c.db.ExecContext(ctx, fmt.Sprintf("SET statement_timeout = %d", d.Milliseconds()))
		return
	}
	_, _ = c.db.ExecContext(ctx, fmt.Sprintf("SET SESSION max_statement_time = %d", int(d.Seconds())))
}

// ---------------------------------------------------------------- export

type ExportOptions struct {
	Tables       []string // empty = every table and view
	Structure    bool
	Data         bool
	DropIfExists bool
}

type limitedWriter struct {
	w    io.Writer
	left int64
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > l.left {
		return 0, errors.New("the export is larger than the 1 GiB limit")
	}
	l.left -= int64(len(p))
	return l.w.Write(p)
}

// ExportSQL writes a SQL dump (structure and/or data) of the chosen tables as
// plain statements that re-import through ImportSQL, phpMyAdmin/mysqldump style.
func (c *EditorConn) ExportSQL(ctx context.Context, out io.Writer, dbName string, o ExportOptions) error {
	if !o.Structure && !o.Data {
		return errors.New("choose structure, data or both")
	}
	w := &limitedWriter{w: out, left: editorExportMaxBytes}
	all, err := c.Tables(ctx)
	if err != nil {
		return err
	}
	byName := map[string]bool{}
	for _, t := range all {
		byName[t.Name] = true
	}
	names := o.Tables
	if len(names) == 0 {
		for _, t := range all {
			names = append(names, t.Name)
		}
	}
	// Gather every table's structure first: the single connection cannot run
	// catalog queries while a data cursor is open.
	type item struct {
		st  *EditorStructure
		ddl []string
	}
	var items []item
	for _, n := range names {
		if !byName[n] {
			return fmt.Errorf("table %q does not exist", n)
		}
		st, err := c.Structure(ctx, n)
		if err != nil {
			return err
		}
		it := item{st: st}
		if o.Structure {
			if it.ddl, err = c.definitionFor(ctx, st, false); err != nil {
				return err
			}
		}
		items = append(items, it)
	}

	q := c.d.quote
	fmt.Fprintf(w, "-- Aegis database export\n-- Database: %s (%s)\n-- Generated: %s\n\n", strings.ReplaceAll(dbName, "\n", " "), c.d.kind, time.Now().UTC().Format(time.RFC3339))
	if c.d.kind == "mariadb" {
		io.WriteString(w, "SET NAMES utf8mb4;\nSET FOREIGN_KEY_CHECKS=0;\nSET SQL_MODE='NO_AUTO_VALUE_ON_ZERO';\n\n")
	}
	// Tables first, views last (a view may depend on tables).
	for _, pass := range []string{"table", "view"} {
		for _, it := range items {
			if it.st.Type != pass {
				continue
			}
			if o.Structure {
				fmt.Fprintf(w, "-- Structure of %s\n", q(it.st.Table))
				if o.DropIfExists {
					kw := "TABLE"
					if pass == "view" {
						kw = "VIEW"
					}
					cascade := ""
					if c.d.kind == "postgres" {
						cascade = " CASCADE"
					}
					fmt.Fprintf(w, "DROP %s IF EXISTS %s%s;\n", kw, q(it.st.Table), cascade)
				}
				for _, s := range it.ddl {
					fmt.Fprintf(w, "%s;\n", strings.TrimSuffix(strings.TrimSpace(s), ";"))
				}
				io.WriteString(w, "\n")
			}
			if o.Data && pass == "table" {
				if err := c.exportTableData(ctx, w, it.st); err != nil {
					return err
				}
			}
		}
	}
	if o.Structure && c.d.kind == "postgres" {
		io.WriteString(w, "-- Foreign keys\n")
		for _, it := range items {
			for _, fk := range it.st.ForeignKeys {
				if it.st.Type == "table" {
					fmt.Fprintf(w, "%s;\n", fkStatement(c.d, it.st.Table, fk))
				}
			}
		}
	}
	if c.d.kind == "mariadb" {
		io.WriteString(w, "\nSET FOREIGN_KEY_CHECKS=1;\n")
	}
	return nil
}

// exportTableData streams a table's rows as batched INSERT statements.
func (c *EditorConn) exportTableData(ctx context.Context, w io.Writer, st *EditorStructure) error {
	table := st.Table
	rows, err := c.db.QueryContext(ctx, "SELECT * FROM "+c.d.quote(table))
	if err != nil {
		return err
	}
	defer rows.Close()
	cts, err := rows.ColumnTypes()
	if err != nil {
		return err
	}
	names := make([]string, len(cts))
	types := make([]string, len(cts))
	for i, ct := range cts {
		names[i] = c.d.quote(ct.Name())
		types[i] = ct.DatabaseTypeName()
	}
	head := "INSERT INTO " + c.d.quote(table) + " (" + strings.Join(names, ", ") + ") VALUES\n"
	fmt.Fprintf(w, "-- Data of %s\n", c.d.quote(table))
	var batch []string
	size := 0
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		_, err := io.WriteString(w, head+strings.Join(batch, ",\n")+";\n")
		batch, size = batch[:0], 0
		return err
	}
	vals := make([]any, len(cts))
	ptrs := make([]any, len(cts))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return err
		}
		parts := make([]string, len(vals))
		for i, v := range vals {
			parts[i] = sqlLiteral(c.d, v, types[i])
		}
		line := "(" + strings.Join(parts, ", ") + ")"
		batch = append(batch, line)
		size += len(line)
		if len(batch) >= editorExportBatchRows || size >= editorExportBatchBytes {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := flush(); err != nil {
		return err
	}
	if c.d.kind == "postgres" {
		// Inserting explicit ids does not advance a serial's sequence: move it past the data.
		for _, col := range st.Columns {
			if col.Extra == "auto_increment" {
				qc, qt := c.d.quote(col.Name), c.d.quote(table)
				fmt.Fprintf(w, "SELECT setval(pg_get_serial_sequence(%s, %s), COALESCE(MAX(%s), 1), MAX(%s) IS NOT NULL) FROM %s;\n",
					quoteSQLString(c.d, qt), quoteSQLString(c.d, col.Name), qc, qc, qt)
			}
		}
	}
	io.WriteString(w, "\n")
	return nil
}

// ExportCSV streams every row of a table as CSV. NULL is written as nullToken
// (default \N) so it stays distinct from an empty string.
func (c *EditorConn) ExportCSV(ctx context.Context, out io.Writer, table string, header bool, nullToken string) error {
	if _, err := c.requireTable(ctx, table); err != nil {
		return err
	}
	if nullToken == "" {
		nullToken = `\N`
	}
	w := csv.NewWriter(&limitedWriter{w: out, left: editorExportMaxBytes})
	rows, err := c.db.QueryContext(ctx, "SELECT * FROM "+c.d.quote(table))
	if err != nil {
		return err
	}
	defer rows.Close()
	cts, err := rows.ColumnTypes()
	if err != nil {
		return err
	}
	if header {
		names := make([]string, len(cts))
		for i, ct := range cts {
			names[i] = ct.Name()
		}
		if err := w.Write(names); err != nil {
			return err
		}
	}
	vals := make([]any, len(cts))
	ptrs := make([]any, len(cts))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	rec := make([]string, len(cts))
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return err
		}
		for i, v := range vals {
			rec[i] = csvValue(v, cts[i].DatabaseTypeName(), nullToken)
		}
		if err := w.Write(rec); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	w.Flush()
	return w.Error()
}

func csvValue(v any, dbType, nullToken string) string {
	switch x := v.(type) {
	case nil:
		return nullToken
	case []byte:
		if isBinaryType(dbType) || !utf8.Valid(x) {
			return "0x" + hex.EncodeToString(x)
		}
		return string(x)
	case string:
		return x
	case time.Time:
		if strings.EqualFold(dbType, "DATE") {
			return x.Format("2006-01-02")
		}
		return x.Format("2006-01-02 15:04:05.999999999")
	case bool:
		return strconv.FormatBool(x)
	default:
		return fmt.Sprint(x)
	}
}

// ---------------------------------------------------------------- import

type ImportResult struct {
	Statements int    `json:"statements"`
	Rows       int64  `json:"rows"`
	Affected   int64  `json:"affected"`
	FailedAt   int    `json:"failed_at,omitempty"` // 1-based statement or row number
	FailedSQL  string `json:"failed_sql,omitempty"`
	Error      string `json:"error,omitempty"`
}

func snippet(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 160 {
		s = s[:160] + "…"
	}
	return s
}

// ImportSQL runs a SQL script (a dump, or hand-written statements). On
// PostgreSQL it is all-or-nothing (one transaction). MySQL cannot roll back DDL,
// so statements before a failure stay applied; foreign key checks are switched
// off for the run so a dump's table order does not matter.
func (c *EditorConn) ImportSQL(ctx context.Context, script string) (*ImportResult, error) {
	stmts := splitStatements(script, c.d.kind)
	res := &ImportResult{}
	if len(stmts) == 0 {
		return res, errors.New("the file contains no SQL statements")
	}
	if len(stmts) > editorMaxImportStatements {
		return res, fmt.Errorf("too many statements (%d); the limit is %d", len(stmts), editorMaxImportStatements)
	}
	type execer interface {
		ExecContext(context.Context, string, ...any) (sql.Result, error)
	}
	var ex execer = c.db
	var tx *sql.Tx
	if c.d.kind == "postgres" {
		var err error
		if tx, err = c.db.BeginTx(ctx, nil); err != nil {
			return res, err
		}
		defer tx.Rollback()
		ex = tx
	} else {
		_, _ = c.db.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS=0")
		defer func() { _, _ = c.db.ExecContext(context.Background(), "SET FOREIGN_KEY_CHECKS=1") }()
	}
	for i, s := range stmts {
		r, err := ex.ExecContext(ctx, s)
		if err != nil {
			res.FailedAt, res.FailedSQL, res.Error = i+1, snippet(s), err.Error()
			if tx != nil {
				res.Statements, res.Affected = 0, 0 // rolled back
			}
			return res, fmt.Errorf("statement %d failed: %w", i+1, err)
		}
		res.Statements++
		if n, err := r.RowsAffected(); err == nil && n > 0 {
			res.Affected += n
		}
	}
	if tx != nil {
		if err := tx.Commit(); err != nil {
			return res, err
		}
	}
	return res, nil
}

type CSVImportOptions struct {
	Header    bool   // the first record names the columns
	NullToken string // a field equal to this becomes NULL (default \N)
	Delimiter rune   // default ','
}

// ImportCSV loads CSV rows into an existing table, in batches inside a single
// transaction (all or nothing). With a header row the columns are matched by
// name; without one the fields map to the table's columns in order.
func (c *EditorConn) ImportCSV(ctx context.Context, in io.Reader, table string, o CSVImportOptions) (*ImportResult, error) {
	res := &ImportResult{}
	st, err := c.writableTable(ctx, table)
	if err != nil {
		return res, err
	}
	if o.NullToken == "" {
		o.NullToken = `\N`
	}
	r := csv.NewReader(in)
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	if o.Delimiter != 0 {
		r.Comma = o.Delimiter
	}
	var cols []string
	have := columnSet(st)
	if o.Header {
		rec, err := r.Read()
		if err != nil {
			return res, fmt.Errorf("could not read the header row: %w", err)
		}
		for _, h := range rec {
			h = strings.TrimPrefix(strings.TrimSpace(h), "\ufeff")
			if !have[h] {
				return res, fmt.Errorf("the header names a column that does not exist: %q", h)
			}
			cols = append(cols, h)
		}
	} else {
		for _, col := range st.Columns {
			cols = append(cols, col.Name)
		}
	}
	if len(cols) == 0 {
		return res, errors.New("no columns to import into")
	}
	perBatch := editorCSVBatchParams / len(cols)
	if perBatch > 500 {
		perBatch = 500
	}
	if perBatch < 1 {
		perBatch = 1
	}
	head := "INSERT INTO " + c.d.quote(table) + " (" + quoteList(c.d, cols) + ") VALUES "
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return res, err
	}
	defer tx.Rollback()

	var batch [][]any
	line := 0
	var inserted int64
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		var sb strings.Builder
		sb.WriteString(head)
		args := make([]any, 0, len(batch)*len(cols))
		n := 0
		for bi, rowVals := range batch {
			if bi > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString("(")
			for ci := range rowVals {
				if ci > 0 {
					sb.WriteString(", ")
				}
				n++
				sb.WriteString(c.d.ph(n))
			}
			sb.WriteString(")")
			args = append(args, rowVals...)
		}
		if _, err := tx.ExecContext(ctx, sb.String(), args...); err != nil {
			return err
		}
		inserted += int64(len(batch))
		batch = batch[:0]
		return nil
	}
	fail := func(err error) (*ImportResult, error) {
		res.FailedAt, res.Error = line, err.Error()
		return res, fmt.Errorf("near row %d: %w", line, err) // the transaction rolls back: nothing was imported
	}
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		line++
		if err != nil {
			return fail(err)
		}
		if len(rec) > len(cols) || (o.Header && len(rec) != len(cols)) {
			return fail(fmt.Errorf("has %d fields, expected %d", len(rec), len(cols)))
		}
		vals := make([]any, len(cols))
		for i := range vals {
			if i < len(rec) && rec[i] != o.NullToken {
				vals[i] = rec[i]
			}
		}
		batch = append(batch, vals)
		if len(batch) >= perBatch {
			if err := flush(); err != nil {
				return fail(err)
			}
		}
	}
	if err := flush(); err != nil {
		return fail(err)
	}
	if err := tx.Commit(); err != nil {
		return res, err
	}
	res.Rows, res.Statements = inserted, 1
	return res, nil
}

// ---------------------------------------------------------------- scripts

// ScriptResult is the outcome of one statement in a multi-statement run.
type ScriptResult struct {
	SQL    string        `json:"sql"`
	Result *EditorResult `json:"result,omitempty"`
	Error  string        `json:"error,omitempty"`
}

// ExecScript runs several statements in order (the SQL tab's multi-statement
// support) and stops at the first error, returning what completed.
//
// before, when set, is called with each statement just before it runs, so the
// caller can audit what was attempted even if the statement fails or hangs.
func (c *EditorConn) ExecScript(ctx context.Context, script string, limit int, before func(stmt string)) ([]ScriptResult, error) {
	stmts := splitStatements(script, c.d.kind)
	if len(stmts) == 0 {
		return nil, errors.New("enter a SQL statement")
	}
	if len(stmts) > editorMaxScriptStatements {
		return nil, fmt.Errorf("at most %d statements can be run at once; use Import for larger scripts", editorMaxScriptStatements)
	}
	out := make([]ScriptResult, 0, len(stmts))
	for _, s := range stmts {
		if before != nil {
			before(s)
		}
		res, err := c.Exec(ctx, s, limit)
		if err != nil {
			out = append(out, ScriptResult{SQL: snippet(s), Error: err.Error()})
			return out, err
		}
		out = append(out, ScriptResult{SQL: snippet(s), Result: res})
	}
	return out, nil
}
