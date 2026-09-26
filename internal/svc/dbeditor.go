package svc

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver

	"aegis/internal/config"
	"aegis/internal/store"
)

// DBEditor is a phpMyAdmin-style editor for a user's MariaDB/MySQL or
// PostgreSQL database: browse tables, inspect structure, edit rows and run SQL.
//
// Every request connects as the database's OWN user (with the credentials the
// panel stores for it), never as the admin account, so the server's own grants
// decide what can be touched and one tenant can never reach another's data
// through the editor. Table and column names are only ever used after being
// checked against the live schema, and values are always bound as parameters.
type DBEditor struct {
	Cfg   *config.Config
	Store *store.Store

	mu   sync.Mutex
	sems map[int64]chan struct{} // per panel user: limits concurrent editor requests
}

func NewDBEditor(cfg *config.Config, st *store.Store) *DBEditor {
	return &DBEditor{Cfg: cfg, Store: st, sems: map[int64]chan struct{}{}}
}

// Limits keep one request from exhausting the panel or the database server.
const (
	editorStatementTimeout = 30 * time.Second
	editorMaxConcurrent    = 3
	editorMaxRows          = 1000      // most rows one SQL-console result returns
	editorMaxBytes         = 8 << 20   // most result bytes read before truncating
	editorMaxCell          = 100 << 10 // longest text value returned in a grid cell
	editorMaxBinaryShown   = 256       // bytes of a binary value shown as hex
	editorMaxFilters       = 10
	editorMaxPageSize      = 200
)

// --- types returned to the API -------------------------------------------------

type EditorTable struct {
	Name      string `json:"name"`
	Type      string `json:"type"` // "table" or "view"
	Rows      int64  `json:"rows"` // an estimate from the catalog, not an exact count
	SizeBytes int64  `json:"size_bytes"`
	Engine    string `json:"engine,omitempty"`
}

type EditorColumn struct {
	Name     string  `json:"name"`
	Type     string  `json:"type"`
	Nullable bool    `json:"nullable"`
	Default  *string `json:"default"`
	Key      string  `json:"key,omitempty"`   // PRI, UNI or MUL
	Extra    string  `json:"extra,omitempty"` // e.g. auto_increment
	Comment  string  `json:"comment,omitempty"`
}

type EditorIndex struct {
	Name    string   `json:"name"`
	Columns []string `json:"columns"`
	Unique  bool     `json:"unique"`
	Primary bool     `json:"primary"`
	Method  string   `json:"method,omitempty"`
}

type EditorForeignKey struct {
	Name       string   `json:"name"`
	Columns    []string `json:"columns"`
	RefTable   string   `json:"ref_table"`
	RefColumns []string `json:"ref_columns"`
}

type EditorStructure struct {
	Table       string             `json:"table"`
	Type        string             `json:"type"`
	Columns     []EditorColumn     `json:"columns"`
	PrimaryKey  []string           `json:"primary_key"`
	Indexes     []EditorIndex      `json:"indexes"`
	ForeignKeys []EditorForeignKey `json:"foreign_keys"`
}

type EditorResultColumn struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// EditorResult is a result grid or the outcome of a statement. Cell values are
// JSON-safe: NULL is null, integers beyond JS's exact range are strings,
// timestamps are RFC 3339 strings and binary data is shown as 0x… hex.
type EditorResult struct {
	Columns   []EditorResultColumn `json:"columns"`
	Rows      [][]any              `json:"rows"`
	Affected  int64                `json:"affected"`
	Total     int64                `json:"total"` // matching rows for a browse; -1 if unknown
	Truncated bool                 `json:"truncated"`
	ElapsedMS int64                `json:"elapsed_ms"`
}

type EditorFilter struct {
	Column string `json:"column"`
	Op     string `json:"op"`
	Value  string `json:"value"`
}

type EditorBrowse struct {
	Table    string
	Page     int
	PageSize int
	Sort     string
	Desc     bool
	Filters  []EditorFilter
}

// --- dialects ---------------------------------------------------------------------

type dialect struct {
	kind  string // "mariadb" or "postgres"
	quote func(string) string
	ph    func(n int) string // n-th bound parameter, 1-based
}

var mysqlDialect = dialect{
	kind:  "mariadb",
	quote: func(s string) string { return "`" + strings.ReplaceAll(s, "`", "``") + "`" },
	ph:    func(int) string { return "?" },
}

var pgDialect = dialect{
	kind:  "postgres",
	quote: func(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` },
	ph:    func(n int) string { return "$" + strconv.Itoa(n) },
}

// --- connection -------------------------------------------------------------------

// EditorConn is one short-lived connection to a user's database, made as that
// database's own user. Close it when done.
type EditorConn struct {
	db      *sql.DB
	d       dialect
	release func()
}

func (c *EditorConn) Close() error {
	err := c.db.Close()
	if c.release != nil {
		c.release()
	}
	return err
}

// Open connects to dbRow as its own database user. It fails fast, without
// waiting, if the panel user already has editorMaxConcurrent requests running.
func (e *DBEditor) Open(ctx context.Context, dbRow *store.Database) (*EditorConn, error) {
	release, err := e.acquire(ctx, dbRow.UserID)
	if err != nil {
		return nil, err
	}
	conn, err := e.connect(ctx, dbRow)
	if err != nil {
		release()
		return nil, err
	}
	conn.release = release
	return conn, nil
}

func (e *DBEditor) acquire(ctx context.Context, userID int64) (func(), error) {
	e.mu.Lock()
	sem, ok := e.sems[userID]
	if !ok {
		sem = make(chan struct{}, editorMaxConcurrent)
		e.sems[userID] = sem
	}
	e.mu.Unlock()
	select {
	case sem <- struct{}{}:
		return func() { <-sem }, nil
	case <-time.After(2 * time.Second):
		return nil, errors.New("too many database editor requests are running for this account; try again in a moment")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (e *DBEditor) connect(ctx context.Context, row *store.Database) (*EditorConn, error) {
	switch row.Server {
	case "mariadb":
		mc := mysql.NewConfig()
		mc.Net = "tcp"
		mc.Addr = fmt.Sprintf("%s:%d", e.Cfg.MariaDB.Host, e.Cfg.MariaDB.Port)
		mc.User = row.DBUser
		mc.Passwd = row.DBPassword
		mc.DBName = row.Name
		mc.AllowNativePasswords = true
		mc.ClientFoundRows = true // UPDATE reports matched rows, so "no change" is not "not found"
		mc.Timeout = 5 * time.Second
		mc.ReadTimeout = editorStatementTimeout + 5*time.Second
		mc.WriteTimeout = 15 * time.Second
		mc.Params = map[string]string{"charset": "utf8mb4"}
		db, err := sql.Open("mysql", mc.FormatDSN())
		if err != nil {
			return nil, err
		}
		db.SetMaxOpenConns(1) // one connection, so the session settings below apply to every statement
		if err := db.PingContext(ctx); err != nil {
			db.Close()
			return nil, fmt.Errorf("could not connect to the database: %w", err)
		}
		// MariaDB's server-side limit; MySQL ignores/rejects it, which is fine.
		_, _ = db.ExecContext(ctx, fmt.Sprintf("SET SESSION max_statement_time = %d", int(editorStatementTimeout.Seconds())))
		return &EditorConn{db: db, d: mysqlDialect}, nil
	case "postgres":
		dsn := fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=disable&connect_timeout=5&statement_timeout=%d",
			url.QueryEscape(row.DBUser), url.QueryEscape(row.DBPassword),
			e.Cfg.PostgreSQL.Host, e.Cfg.PostgreSQL.Port, url.PathEscape(row.Name), editorStatementTimeout.Milliseconds())
		db, err := sql.Open("pgx", dsn)
		if err != nil {
			return nil, err
		}
		db.SetMaxOpenConns(1)
		if err := db.PingContext(ctx); err != nil {
			db.Close()
			return nil, fmt.Errorf("could not connect to the database: %w", err)
		}
		return &EditorConn{db: db, d: pgDialect}, nil
	default:
		return nil, fmt.Errorf("unsupported database server %q", row.Server)
	}
}

// --- schema -----------------------------------------------------------------------

// Tables lists the tables and views of the database (the "public" schema on
// PostgreSQL).
func (c *EditorConn) Tables(ctx context.Context) ([]EditorTable, error) {
	var q string
	if c.d.kind == "postgres" {
		q = `SELECT c.relname,
			CASE WHEN c.relkind IN ('v','m') THEN 'view' ELSE 'table' END,
			GREATEST(c.reltuples, 0)::bigint,
			pg_total_relation_size(c.oid)::bigint,
			''
		FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'public' AND c.relkind IN ('r','p','v','m')
		ORDER BY c.relname`
	} else {
		q = `SELECT TABLE_NAME,
			IF(TABLE_TYPE = 'VIEW', 'view', 'table'),
			CAST(IFNULL(TABLE_ROWS, 0) AS SIGNED),
			CAST(IFNULL(DATA_LENGTH, 0) + IFNULL(INDEX_LENGTH, 0) AS SIGNED),
			IFNULL(ENGINE, '')
		FROM information_schema.TABLES
		WHERE TABLE_SCHEMA = DATABASE()
		ORDER BY TABLE_NAME`
	}
	rows, err := c.db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []EditorTable{}
	for rows.Next() {
		var t EditorTable
		if err := rows.Scan(&t.Name, &t.Type, &t.Rows, &t.SizeBytes, &t.Engine); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// requireTable checks that name is a real table or view and returns its type;
// this is what makes it safe to quote and use the name in a statement.
func (c *EditorConn) requireTable(ctx context.Context, name string) (string, error) {
	tables, err := c.Tables(ctx)
	if err != nil {
		return "", err
	}
	for _, t := range tables {
		if t.Name == name {
			return t.Type, nil
		}
	}
	return "", fmt.Errorf("table %q does not exist", name)
}

// Structure describes a table: columns, primary key, indexes and foreign keys.
func (c *EditorConn) Structure(ctx context.Context, table string) (*EditorStructure, error) {
	typ, err := c.requireTable(ctx, table)
	if err != nil {
		return nil, err
	}
	st := &EditorStructure{Table: table, Type: typ, Columns: []EditorColumn{}, PrimaryKey: []string{}, Indexes: []EditorIndex{}, ForeignKeys: []EditorForeignKey{}}
	if c.d.kind == "postgres" {
		err = c.structurePG(ctx, st)
	} else {
		err = c.structureMySQL(ctx, st)
	}
	if err != nil {
		return nil, err
	}
	for _, ix := range st.Indexes {
		if ix.Primary {
			st.PrimaryKey = ix.Columns
		}
	}
	return st, nil
}

func splitCols(s sql.NullString) []string {
	if !s.Valid || s.String == "" {
		return []string{}
	}
	return strings.Split(s.String, ",")
}

func (c *EditorConn) structureMySQL(ctx context.Context, st *EditorStructure) error {
	rows, err := c.db.QueryContext(ctx, `SELECT COLUMN_NAME, COLUMN_TYPE, IS_NULLABLE = 'YES', COLUMN_DEFAULT, COLUMN_KEY, EXTRA, COLUMN_COMMENT
		FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? ORDER BY ORDINAL_POSITION`, st.Table)
	if err != nil {
		return err
	}
	for rows.Next() {
		var col EditorColumn
		var def sql.NullString
		if err := rows.Scan(&col.Name, &col.Type, &col.Nullable, &def, &col.Key, &col.Extra, &col.Comment); err != nil {
			rows.Close()
			return err
		}
		if def.Valid {
			d := def.String
			col.Default = &d
		}
		st.Columns = append(st.Columns, col)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	irows, err := c.db.QueryContext(ctx, `SELECT INDEX_NAME, NON_UNIQUE = 0, GROUP_CONCAT(COLUMN_NAME ORDER BY SEQ_IN_INDEX), INDEX_TYPE
		FROM information_schema.STATISTICS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ?
		GROUP BY INDEX_NAME, NON_UNIQUE, INDEX_TYPE ORDER BY INDEX_NAME = 'PRIMARY' DESC, INDEX_NAME`, st.Table)
	if err != nil {
		return err
	}
	for irows.Next() {
		var ix EditorIndex
		var cols sql.NullString
		if err := irows.Scan(&ix.Name, &ix.Unique, &cols, &ix.Method); err != nil {
			irows.Close()
			return err
		}
		ix.Columns = splitCols(cols)
		ix.Primary = ix.Name == "PRIMARY"
		st.Indexes = append(st.Indexes, ix)
	}
	irows.Close()
	if err := irows.Err(); err != nil {
		return err
	}
	frows, err := c.db.QueryContext(ctx, `SELECT CONSTRAINT_NAME, GROUP_CONCAT(COLUMN_NAME ORDER BY ORDINAL_POSITION), REFERENCED_TABLE_NAME,
		GROUP_CONCAT(REFERENCED_COLUMN_NAME ORDER BY ORDINAL_POSITION)
		FROM information_schema.KEY_COLUMN_USAGE
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND REFERENCED_TABLE_NAME IS NOT NULL
		GROUP BY CONSTRAINT_NAME, REFERENCED_TABLE_NAME ORDER BY CONSTRAINT_NAME`, st.Table)
	if err != nil {
		return err
	}
	defer frows.Close()
	for frows.Next() {
		var fk EditorForeignKey
		var cols, refs sql.NullString
		if err := frows.Scan(&fk.Name, &cols, &fk.RefTable, &refs); err != nil {
			return err
		}
		fk.Columns, fk.RefColumns = splitCols(cols), splitCols(refs)
		st.ForeignKeys = append(st.ForeignKeys, fk)
	}
	return frows.Err()
}

func (c *EditorConn) structurePG(ctx context.Context, st *EditorStructure) error {
	reg := c.d.quote(st.Table) // resolved through the search path ("public")
	rows, err := c.db.QueryContext(ctx, `SELECT a.attname, format_type(a.atttypid, a.atttypmod), NOT a.attnotnull,
			pg_get_expr(d.adbin, d.adrelid), COALESCE(col_description(a.attrelid, a.attnum), ''),
			(a.attidentity <> '' OR COALESCE(pg_get_expr(d.adbin, d.adrelid), '') LIKE 'nextval(%')
		FROM pg_attribute a LEFT JOIN pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
		WHERE a.attrelid = $1::regclass AND a.attnum > 0 AND NOT a.attisdropped ORDER BY a.attnum`, reg)
	if err != nil {
		return err
	}
	for rows.Next() {
		var col EditorColumn
		var def sql.NullString
		var auto bool
		if err := rows.Scan(&col.Name, &col.Type, &col.Nullable, &def, &col.Comment, &auto); err != nil {
			rows.Close()
			return err
		}
		if def.Valid {
			d := def.String
			col.Default = &d
		}
		if auto {
			col.Extra = "auto_increment"
		}
		st.Columns = append(st.Columns, col)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	irows, err := c.db.QueryContext(ctx, `SELECT i.relname, ix.indisunique, ix.indisprimary,
			(SELECT string_agg(a.attname, ',' ORDER BY k.ord) FROM unnest(ix.indkey) WITH ORDINALITY k(attnum, ord)
			   JOIN pg_attribute a ON a.attrelid = ix.indrelid AND a.attnum = k.attnum),
			am.amname
		FROM pg_index ix JOIN pg_class i ON i.oid = ix.indexrelid JOIN pg_am am ON am.oid = i.relam
		WHERE ix.indrelid = $1::regclass ORDER BY ix.indisprimary DESC, i.relname`, reg)
	if err != nil {
		return err
	}
	for irows.Next() {
		var ix EditorIndex
		var cols sql.NullString
		if err := irows.Scan(&ix.Name, &ix.Unique, &ix.Primary, &cols, &ix.Method); err != nil {
			irows.Close()
			return err
		}
		ix.Columns = splitCols(cols)
		st.Indexes = append(st.Indexes, ix)
	}
	irows.Close()
	if err := irows.Err(); err != nil {
		return err
	}
	// Key badges, as MySQL's COLUMN_KEY shows them.
	for i := range st.Columns {
		for _, ix := range st.Indexes {
			for _, cn := range ix.Columns {
				if cn != st.Columns[i].Name {
					continue
				}
				switch {
				case ix.Primary:
					st.Columns[i].Key = "PRI"
				case ix.Unique && len(ix.Columns) == 1 && st.Columns[i].Key == "":
					st.Columns[i].Key = "UNI"
				case st.Columns[i].Key == "":
					st.Columns[i].Key = "MUL"
				}
			}
		}
	}
	frows, err := c.db.QueryContext(ctx, `SELECT con.conname,
			(SELECT string_agg(a.attname, ',' ORDER BY k.ord) FROM unnest(con.conkey) WITH ORDINALITY k(attnum, ord)
			   JOIN pg_attribute a ON a.attrelid = con.conrelid AND a.attnum = k.attnum),
			cl.relname,
			(SELECT string_agg(a.attname, ',' ORDER BY k.ord) FROM unnest(con.confkey) WITH ORDINALITY k(attnum, ord)
			   JOIN pg_attribute a ON a.attrelid = con.confrelid AND a.attnum = k.attnum)
		FROM pg_constraint con JOIN pg_class cl ON cl.oid = con.confrelid
		WHERE con.contype = 'f' AND con.conrelid = $1::regclass ORDER BY con.conname`, reg)
	if err != nil {
		return err
	}
	defer frows.Close()
	for frows.Next() {
		var fk EditorForeignKey
		var cols, refs sql.NullString
		if err := frows.Scan(&fk.Name, &cols, &fk.RefTable, &refs); err != nil {
			return err
		}
		fk.Columns, fk.RefColumns = splitCols(cols), splitCols(refs)
		st.ForeignKeys = append(st.ForeignKeys, fk)
	}
	return frows.Err()
}

// --- browsing ---------------------------------------------------------------------

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// buildWhere turns filters into a parameterised WHERE clause. Columns must be
// real columns of the table and operators come from a fixed list, so nothing a
// client sends is ever pasted into SQL.
func buildWhere(d dialect, cols map[string]bool, filters []EditorFilter) (string, []any, error) {
	if len(filters) > editorMaxFilters {
		return "", nil, fmt.Errorf("at most %d filters are allowed", editorMaxFilters)
	}
	var parts []string
	var args []any
	for _, f := range filters {
		if !cols[f.Column] {
			return "", nil, fmt.Errorf("unknown column %q", f.Column)
		}
		q := d.quote(f.Column)
		add := func(expr, op, val string) {
			args = append(args, val)
			parts = append(parts, expr+" "+op+" "+d.ph(len(args)))
		}
		textExpr := q
		if d.kind == "postgres" {
			textExpr = "CAST(" + q + " AS TEXT)" // LIKE on a non-text column would otherwise error
		}
		switch op := strings.ToUpper(strings.TrimSpace(f.Op)); op {
		case "IS NULL", "IS NOT NULL":
			parts = append(parts, q+" "+op)
		case "=", "!=", "<", ">", "<=", ">=":
			add(q, op, f.Value)
		case "LIKE", "NOT LIKE":
			add(textExpr, op, f.Value)
		case "CONTAINS":
			add(textExpr, "LIKE", "%"+escapeLike(f.Value)+"%")
		default:
			return "", nil, fmt.Errorf("unsupported filter operator %q", f.Op)
		}
	}
	if len(parts) == 0 {
		return "", nil, nil
	}
	return " WHERE " + strings.Join(parts, " AND "), args, nil
}

func columnSet(st *EditorStructure) map[string]bool {
	m := make(map[string]bool, len(st.Columns))
	for _, c := range st.Columns {
		m[c.Name] = true
	}
	return m
}

// Browse returns one page of a table's rows, with optional sort and filters.
func (c *EditorConn) Browse(ctx context.Context, req EditorBrowse) (*EditorResult, error) {
	st, err := c.Structure(ctx, req.Table)
	if err != nil {
		return nil, err
	}
	cols := columnSet(st)
	where, args, err := buildWhere(c.d, cols, req.Filters)
	if err != nil {
		return nil, err
	}
	size := req.PageSize
	if size < 1 {
		size = 50
	}
	if size > editorMaxPageSize {
		size = editorMaxPageSize
	}
	page := req.Page
	if page < 1 {
		page = 1
	}
	order := ""
	switch {
	case req.Sort != "":
		if !cols[req.Sort] {
			return nil, fmt.Errorf("unknown column %q", req.Sort)
		}
		dir := " ASC"
		if req.Desc {
			dir = " DESC"
		}
		order = " ORDER BY " + c.d.quote(req.Sort) + dir
	case len(st.PrimaryKey) > 0: // a stable order so paging is consistent
		qs := make([]string, len(st.PrimaryKey))
		for i, k := range st.PrimaryKey {
			qs[i] = c.d.quote(k)
		}
		order = " ORDER BY " + strings.Join(qs, ", ")
	}
	tbl := c.d.quote(req.Table)

	start := time.Now()
	total := int64(-1)
	if err := c.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+tbl+where, args...).Scan(&total); err != nil {
		total = -1 // a slow count must not stop the page from loading
	}
	q := fmt.Sprintf("SELECT * FROM %s%s%s LIMIT %d OFFSET %d", tbl, where, order, size, (page-1)*size)
	rows, err := c.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	res, err := scanRows(rows, size, editorMaxBytes)
	if err != nil {
		return nil, err
	}
	res.Total = total
	res.ElapsedMS = time.Since(start).Milliseconds()
	return res, nil
}

// --- value handling ---------------------------------------------------------------

func isBinaryType(t string) bool {
	switch strings.ToUpper(t) {
	case "BLOB", "TINYBLOB", "MEDIUMBLOB", "LONGBLOB", "BINARY", "VARBINARY", "BYTEA":
		return true
	}
	return false
}

func truncateText(s string) string {
	if len(s) <= editorMaxCell {
		return s
	}
	cut := editorMaxCell
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…[truncated, " + strconv.Itoa(len(s)) + " bytes]"
}

// normalizeValue makes a scanned database value safe to put in JSON.
func normalizeValue(v any, dbType string) any {
	switch x := v.(type) {
	case nil:
		return nil
	case []byte:
		if isBinaryType(dbType) || !utf8.Valid(x) {
			shown := x
			if len(shown) > editorMaxBinaryShown {
				shown = shown[:editorMaxBinaryShown]
			}
			s := "0x" + hex.EncodeToString(shown)
			if len(x) > len(shown) {
				s += "…(" + strconv.Itoa(len(x)) + " bytes)"
			}
			return s
		}
		return truncateText(string(x))
	case string:
		return truncateText(x)
	case time.Time:
		return x.Format(time.RFC3339Nano)
	case int64:
		if x > 1<<53 || x < -(1<<53) {
			return strconv.FormatInt(x, 10) // beyond what a JS number holds exactly
		}
		return x
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return strconv.FormatFloat(x, 'g', -1, 64)
		}
		return x
	case float32:
		return normalizeValue(float64(x), dbType)
	case bool:
		return x
	default:
		return fmt.Sprint(x)
	}
}

func valueSize(v any) int {
	if s, ok := v.(string); ok {
		return len(s)
	}
	return 8
}

// scanRows reads up to maxRows rows (and about maxBytes bytes) from rows.
func scanRows(rows *sql.Rows, maxRows, maxBytes int) (*EditorResult, error) {
	cts, err := rows.ColumnTypes()
	if err != nil {
		return nil, err
	}
	res := &EditorResult{Columns: make([]EditorResultColumn, len(cts)), Rows: [][]any{}, Total: -1}
	types := make([]string, len(cts))
	for i, ct := range cts {
		types[i] = ct.DatabaseTypeName()
		res.Columns[i] = EditorResultColumn{Name: ct.Name(), Type: types[i]}
	}
	bytesRead := 0
	for rows.Next() {
		if len(res.Rows) >= maxRows || bytesRead > maxBytes {
			res.Truncated = true
			break
		}
		vals := make([]any, len(cts))
		ptrs := make([]any, len(cts))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := make([]any, len(cts))
		for i := range vals {
			row[i] = normalizeValue(vals[i], types[i])
			bytesRead += valueSize(row[i])
		}
		res.Rows = append(res.Rows, row)
	}
	return res, rows.Err()
}

// toArg converts a JSON value from the client into a bound query argument.
// Numbers and booleans are sent as text so the server parses them as the
// column's own type (a JSON 5 for a BIGINT, a true for a BOOLEAN).
func toArg(d dialect, v any) (any, error) {
	switch x := v.(type) {
	case nil:
		return nil, nil
	case string:
		return x, nil
	case bool:
		if d.kind == "postgres" {
			return strconv.FormatBool(x), nil
		}
		if x {
			return "1", nil
		}
		return "0", nil
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64), nil
	case json.Number:
		return x.String(), nil // exact text, so integers beyond 2^53 are not rounded
	case int:
		return strconv.Itoa(x), nil
	case int64:
		return strconv.FormatInt(x, 10), nil
	default:
		return nil, fmt.Errorf("unsupported value type %T", v)
	}
}

// --- row editing ------------------------------------------------------------------

// keyWhere builds "WHERE pk = ? AND ..." from a row's primary key values; the
// key must name exactly the table's primary key columns.
func (c *EditorConn) keyWhere(st *EditorStructure, key map[string]any, startArg int) (string, []any, error) {
	if len(st.PrimaryKey) == 0 {
		return "", nil, errors.New("this table has no primary key, so single rows cannot be edited here; use the SQL tab")
	}
	if len(key) != len(st.PrimaryKey) {
		return "", nil, errors.New("the row key must contain exactly the primary key columns")
	}
	parts := make([]string, 0, len(st.PrimaryKey))
	args := make([]any, 0, len(st.PrimaryKey))
	for _, k := range st.PrimaryKey {
		v, ok := key[k]
		if !ok {
			return "", nil, fmt.Errorf("missing primary key column %q", k)
		}
		if v == nil {
			return "", nil, fmt.Errorf("primary key column %q cannot be NULL", k)
		}
		a, err := toArg(c.d, v)
		if err != nil {
			return "", nil, err
		}
		args = append(args, a)
		parts = append(parts, c.d.quote(k)+" = "+c.d.ph(startArg+len(args)-1))
	}
	return " WHERE " + strings.Join(parts, " AND "), args, nil
}

func (c *EditorConn) writableTable(ctx context.Context, table string) (*EditorStructure, error) {
	st, err := c.Structure(ctx, table)
	if err != nil {
		return nil, err
	}
	if st.Type == "view" {
		return nil, errors.New("views cannot be edited row by row")
	}
	return st, nil
}

// GetRow returns one whole row (without the grid's cell truncation), for the edit form.
func (c *EditorConn) GetRow(ctx context.Context, table string, key map[string]any) (map[string]any, error) {
	st, err := c.writableTable(ctx, table)
	if err != nil {
		return nil, err
	}
	where, args, err := c.keyWhere(st, key, 1)
	if err != nil {
		return nil, err
	}
	rows, err := c.db.QueryContext(ctx, "SELECT * FROM "+c.d.quote(table)+where+" LIMIT 1", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	res, err := scanRows(rows, 1, editorMaxBytes*2)
	if err != nil {
		return nil, err
	}
	if len(res.Rows) == 0 {
		return nil, errors.New("row not found")
	}
	out := make(map[string]any, len(res.Columns))
	for i, col := range res.Columns {
		out[col.Name] = res.Rows[0][i]
	}
	return out, nil
}

func (c *EditorConn) valuesToArgs(st *EditorStructure, values map[string]any) ([]string, []any, error) {
	cols := columnSet(st)
	for k := range values {
		if !cols[k] {
			return nil, nil, fmt.Errorf("unknown column %q", k)
		}
	}
	// Keep the column order the table defines, so statements are deterministic.
	ordered := make([]string, 0, len(values))
	for _, col := range st.Columns {
		if _, ok := values[col.Name]; ok {
			ordered = append(ordered, col.Name)
		}
	}
	args := make([]any, 0, len(ordered))
	for _, n := range ordered {
		a, err := toArg(c.d, values[n])
		if err != nil {
			return nil, nil, fmt.Errorf("column %q: %w", n, err)
		}
		args = append(args, a)
	}
	return ordered, args, nil
}

// Insert adds a row. Columns not in values take their defaults.
func (c *EditorConn) Insert(ctx context.Context, table string, values map[string]any) (int64, error) {
	st, err := c.writableTable(ctx, table)
	if err != nil {
		return 0, err
	}
	names, args, err := c.valuesToArgs(st, values)
	if err != nil {
		return 0, err
	}
	tbl := c.d.quote(table)
	var q string
	if len(names) == 0 {
		if c.d.kind == "postgres" {
			q = "INSERT INTO " + tbl + " DEFAULT VALUES"
		} else {
			q = "INSERT INTO " + tbl + " () VALUES ()"
		}
	} else {
		qs := make([]string, len(names))
		phs := make([]string, len(names))
		for i, n := range names {
			qs[i] = c.d.quote(n)
			phs[i] = c.d.ph(i + 1)
		}
		q = "INSERT INTO " + tbl + " (" + strings.Join(qs, ", ") + ") VALUES (" + strings.Join(phs, ", ") + ")"
	}
	res, err := c.db.ExecContext(ctx, q, args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// Update changes one row, found by its primary key.
func (c *EditorConn) Update(ctx context.Context, table string, key, values map[string]any) (int64, error) {
	st, err := c.writableTable(ctx, table)
	if err != nil {
		return 0, err
	}
	if len(values) == 0 {
		return 0, errors.New("nothing to update")
	}
	names, args, err := c.valuesToArgs(st, values)
	if err != nil {
		return 0, err
	}
	sets := make([]string, len(names))
	for i, n := range names {
		sets[i] = c.d.quote(n) + " = " + c.d.ph(i+1)
	}
	where, kargs, err := c.keyWhere(st, key, len(args)+1)
	if err != nil {
		return 0, err
	}
	res, err := c.db.ExecContext(ctx, "UPDATE "+c.d.quote(table)+" SET "+strings.Join(sets, ", ")+where, append(args, kargs...)...)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err == nil && n == 0 {
		return 0, errors.New("row not found")
	}
	return n, err
}

// Delete removes one row, found by its primary key.
func (c *EditorConn) Delete(ctx context.Context, table string, key map[string]any) (int64, error) {
	st, err := c.writableTable(ctx, table)
	if err != nil {
		return 0, err
	}
	where, args, err := c.keyWhere(st, key, 1)
	if err != nil {
		return 0, err
	}
	res, err := c.db.ExecContext(ctx, "DELETE FROM "+c.d.quote(table)+where, args...)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err == nil && n == 0 {
		return 0, errors.New("row not found")
	}
	return n, err
}

// Truncate empties a table; Drop deletes a table or view. Both are destructive,
// so callers confirm with the user first.
func (c *EditorConn) Truncate(ctx context.Context, table string) error {
	typ, err := c.requireTable(ctx, table)
	if err != nil {
		return err
	}
	if typ == "view" {
		return errors.New("a view cannot be truncated")
	}
	_, err = c.db.ExecContext(ctx, "TRUNCATE TABLE "+c.d.quote(table))
	return err
}

func (c *EditorConn) Drop(ctx context.Context, table string) error {
	typ, err := c.requireTable(ctx, table)
	if err != nil {
		return err
	}
	kw := "TABLE"
	if typ == "view" {
		kw = "VIEW"
	}
	_, err = c.db.ExecContext(ctx, "DROP "+kw+" "+c.d.quote(table))
	return err
}

// --- SQL console ------------------------------------------------------------------

// statementReturnsRows reports whether sqlText is a statement that produces a
// result set (as opposed to a row count).
func statementReturnsRows(d dialect, sqlText string) bool {
	fields := strings.Fields(strings.ToUpper(sqlText))
	if len(fields) == 0 {
		return false
	}
	switch fields[0] {
	case "SELECT", "SHOW", "DESCRIBE", "DESC", "EXPLAIN", "WITH", "VALUES", "TABLE", "PRAGMA":
		return true
	}
	if d.kind == "postgres" {
		for _, f := range fields {
			if f == "RETURNING" {
				return true
			}
		}
	}
	return false
}

// Exec runs one SQL statement as the database's own user. The user's grants
// (and the connection being bound to this one database) are the boundary, just
// as in phpMyAdmin's SQL tab. Only a single statement is accepted.
func (c *EditorConn) Exec(ctx context.Context, sqlText string, limit int) (*EditorResult, error) {
	sqlText = strings.TrimSpace(sqlText)
	sqlText = strings.TrimSpace(strings.TrimSuffix(sqlText, ";"))
	if sqlText == "" {
		return nil, errors.New("enter a SQL statement")
	}
	if limit < 1 || limit > editorMaxRows {
		limit = editorMaxRows
	}
	start := time.Now()
	if statementReturnsRows(c.d, sqlText) {
		rows, err := c.db.QueryContext(ctx, sqlText)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		res, err := scanRows(rows, limit, editorMaxBytes)
		if err != nil {
			return nil, err
		}
		res.ElapsedMS = time.Since(start).Milliseconds()
		return res, nil
	}
	r, err := c.db.ExecContext(ctx, sqlText)
	if err != nil {
		return nil, err
	}
	affected, _ := r.RowsAffected()
	return &EditorResult{Columns: []EditorResultColumn{}, Rows: [][]any{}, Affected: affected, Total: -1, ElapsedMS: time.Since(start).Milliseconds()}, nil
}
