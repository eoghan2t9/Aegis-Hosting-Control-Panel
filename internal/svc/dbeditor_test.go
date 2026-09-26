package svc

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// --- pure unit tests -------------------------------------------------------------

func TestEditorQuoting(t *testing.T) {
	for _, c := range []struct {
		d    dialect
		in   string
		want string
	}{
		{mysqlDialect, "users", "`users`"},
		{mysqlDialect, "a`b", "`a``b`"},
		{mysqlDialect, "x`; DROP TABLE t; --", "`x``; DROP TABLE t; --`"},
		{pgDialect, "users", `"users"`},
		{pgDialect, `a"b`, `"a""b"`},
		{pgDialect, `x"; DROP TABLE t; --`, `"x""; DROP TABLE t; --"`},
	} {
		if got := c.d.quote(c.in); got != c.want {
			t.Errorf("%s quote(%q) = %s, want %s", c.d.kind, c.in, got, c.want)
		}
	}
	if mysqlDialect.ph(3) != "?" || pgDialect.ph(3) != "$3" {
		t.Error("placeholder styles are wrong")
	}
}

func TestBuildWhere(t *testing.T) {
	cols := map[string]bool{"id": true, "name": true, "we`ird": true}

	got, args, err := buildWhere(mysqlDialect, cols, []EditorFilter{
		{Column: "id", Op: ">=", Value: "5"},
		{Column: "name", Op: "contains", Value: "50%_off"},
		{Column: "name", Op: "IS NULL"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != " WHERE `id` >= ? AND `name` LIKE ? AND `name` IS NULL" {
		t.Errorf("mysql where = %q", got)
	}
	if len(args) != 2 || args[0] != "5" || args[1] != `%50\%\_off%` {
		t.Errorf("mysql args = %#v (LIKE wildcards in the value must be escaped)", args)
	}

	got, args, err = buildWhere(pgDialect, cols, []EditorFilter{
		{Column: "id", Op: "=", Value: "7"},
		{Column: "name", Op: "LIKE", Value: "a%"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != ` WHERE "id" = $1 AND CAST("name" AS TEXT) LIKE $2` || len(args) != 2 {
		t.Errorf("postgres where = %q args %#v", got, args)
	}

	if got, _, err := buildWhere(mysqlDialect, cols, []EditorFilter{{Column: "we`ird", Op: "="}}); err != nil || !strings.Contains(got, "`we``ird`") {
		t.Errorf("odd column names must be quoted: %q, %v", got, err)
	}

	// Anything that is not a real column or a listed operator is refused, so
	// nothing a client sends is ever pasted into SQL.
	tooMany := make([]EditorFilter, editorMaxFilters+1)
	for i := range tooMany {
		tooMany[i] = EditorFilter{Column: "id", Op: "="}
	}
	bad := [][]EditorFilter{
		{{Column: "nope", Op: "="}},
		{{Column: "id) OR 1=1 --", Op: "="}},
		{{Column: "id", Op: "= 1 OR 1 = 1 --"}},
		{{Column: "id", Op: "UNION SELECT"}},
		tooMany,
	}
	for i, f := range bad {
		if _, _, err := buildWhere(mysqlDialect, cols, f); err == nil {
			t.Errorf("bad filter set %d was accepted", i)
		}
	}
	if w, a, err := buildWhere(mysqlDialect, cols, nil); err != nil || w != "" || a != nil {
		t.Errorf("no filters = %q %v %v", w, a, err)
	}
}

func TestNormalizeValue(t *testing.T) {
	if normalizeValue(nil, "INT") != nil {
		t.Error("NULL must stay nil")
	}
	if got := normalizeValue([]byte("héllo"), "VARCHAR"); got != "héllo" {
		t.Errorf("text bytes = %v", got)
	}
	if got := normalizeValue([]byte{0xde, 0xad, 0xbe, 0xef}, "BLOB"); got != "0xdeadbeef" {
		t.Errorf("blob = %v", got)
	}
	if got := normalizeValue([]byte{0xff, 0xfe}, "VARCHAR"); got != "0xfffe" {
		t.Errorf("invalid utf-8 must be shown as hex, got %v", got)
	}
	long := make([]byte, editorMaxBinaryShown+50)
	if got := normalizeValue(long, "BLOB").(string); !strings.Contains(got, "bytes)") {
		t.Errorf("long blob not summarised: %.60s", got)
	}
	if got := normalizeValue(int64(1<<53+1), "BIGINT"); got != "9007199254740993" {
		t.Errorf("a bigint beyond JS precision must become a string, got %v", got)
	}
	if got := normalizeValue(int64(42), "INT"); got != int64(42) {
		t.Errorf("small ints stay numbers, got %v", got)
	}
	huge := strings.Repeat("x", editorMaxCell+10)
	if got := normalizeValue(huge, "TEXT").(string); len(got) > editorMaxCell+80 || !strings.Contains(got, "truncated") {
		t.Errorf("oversized text not truncated (len %d)", len(got))
	}
	if got := normalizeValue(time.Date(2026, 9, 26, 12, 30, 0, 0, time.UTC), "TIMESTAMP"); got != "2026-09-26T12:30:00Z" {
		t.Errorf("time = %v", got)
	}
}

func TestToArg(t *testing.T) {
	for _, c := range []struct {
		d    dialect
		in   any
		want any
	}{
		{mysqlDialect, nil, nil},
		{mysqlDialect, "x", "x"},
		{mysqlDialect, true, "1"},
		{mysqlDialect, false, "0"},
		{pgDialect, true, "true"},
		{pgDialect, float64(5), "5"},
		{pgDialect, 12.5, "12.5"},
		{pgDialect, int64(7), "7"},
		{pgDialect, json.Number("12345678901234567"), "12345678901234567"}, // no float64 rounding
		{mysqlDialect, 3, "3"},
	} {
		got, err := toArg(c.d, c.in)
		if err != nil || got != c.want {
			t.Errorf("toArg(%s, %#v) = %#v, %v; want %#v", c.d.kind, c.in, got, err, c.want)
		}
	}
	if _, err := toArg(mysqlDialect, map[string]any{"a": 1}); err == nil {
		t.Error("a nested JSON value must be refused")
	}
}

func TestStatementReturnsRows(t *testing.T) {
	for stmt, want := range map[string]bool{
		"SELECT 1": true, "  select * from t": true, "SHOW TABLES": true, "EXPLAIN SELECT 1": true,
		"WITH x AS (SELECT 1) SELECT * FROM x": true, "INSERT INTO t VALUES (1)": false,
		"UPDATE t SET a=1": false, "CREATE TABLE t (a int)": false, "DROP TABLE t": false, "": false,
	} {
		if got := statementReturnsRows(mysqlDialect, stmt); got != want {
			t.Errorf("statementReturnsRows(%q) = %v, want %v", stmt, got, want)
		}
	}
	if !statementReturnsRows(pgDialect, "INSERT INTO t VALUES (1) RETURNING id") {
		t.Error("a Postgres INSERT ... RETURNING returns rows")
	}
}

// --- integration tests against real servers --------------------------------------
//
// Run with a throwaway database and user, e.g.
//   AEGIS_IT_MYSQL_DSN='user:pass@tcp(127.0.0.1:3306)/db?clientFoundRows=true'
//   AEGIS_IT_PG_DSN='postgres://user:pass@127.0.0.1:5432/db?sslmode=disable'
// They create and drop tables named ed_*; skipped when the variables are unset.

func itConn(t *testing.T, kind string) *EditorConn {
	t.Helper()
	var envName, driver string
	var d dialect
	if kind == "mysql" {
		envName, driver, d = "AEGIS_IT_MYSQL_DSN", "mysql", mysqlDialect
	} else {
		envName, driver, d = "AEGIS_IT_PG_DSN", "pgx", pgDialect
	}
	dsn := os.Getenv(envName)
	if dsn == "" {
		t.Skipf("%s not set", envName)
	}
	db, err := sql.Open(driver, dsn)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if err := db.Ping(); err != nil {
		t.Fatalf("cannot reach the test database: %v", err)
	}
	return &EditorConn{db: db, d: d}
}

func setupIT(t *testing.T, c *EditorConn) {
	t.Helper()
	ctx := context.Background()
	exec := func(q string) {
		t.Helper()
		if _, err := c.db.ExecContext(ctx, q); err != nil {
			t.Fatalf("setup %q: %v", q, err)
		}
	}
	drop := func() {
		for _, q := range []string{"DROP VIEW IF EXISTS ed_view", "DROP TABLE IF EXISTS ed_child", "DROP TABLE IF EXISTS ed_items", "DROP TABLE IF EXISTS ed_made", "DROP TABLE IF EXISTS " + c.d.quote("ed weird")} {
			_, _ = c.db.ExecContext(ctx, q)
		}
	}
	drop()
	weirdCol := c.d.quote(`we"ird col`)
	if c.d.kind == "postgres" {
		exec(`CREATE TABLE ed_items (id SERIAL PRIMARY KEY, name VARCHAR(50) NOT NULL UNIQUE, price NUMERIC(10,2), big BIGINT, note TEXT, blobcol BYTEA, created TIMESTAMP)`)
		exec(`CREATE TABLE ed_child (id INT PRIMARY KEY, item_id INT, CONSTRAINT fk_item FOREIGN KEY (item_id) REFERENCES ed_items(id))`)
	} else {
		exec("CREATE TABLE ed_items (id INT AUTO_INCREMENT PRIMARY KEY, name VARCHAR(50) NOT NULL, price DECIMAL(10,2), big BIGINT, note TEXT, blobcol BLOB, created DATETIME, UNIQUE KEY uq_name (name))")
		exec("CREATE TABLE ed_child (id INT PRIMARY KEY, item_id INT, CONSTRAINT fk_item FOREIGN KEY (item_id) REFERENCES ed_items(id))")
	}
	exec("CREATE TABLE " + c.d.quote("ed weird") + " (" + weirdCol + " INT PRIMARY KEY)")
	exec("CREATE VIEW ed_view AS SELECT id, name FROM ed_items")
	t.Cleanup(drop)
}

func TestEditorMySQL(t *testing.T)    { runEditorIT(t, itConn(t, "mysql")) }
func TestEditorPostgres(t *testing.T) { runEditorIT(t, itConn(t, "pg")) }

func runEditorIT(t *testing.T, c *EditorConn) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	setupIT(t, c)

	// --- schema ---
	tables, err := c.Tables(ctx)
	if err != nil {
		t.Fatal(err)
	}
	types := map[string]string{}
	for _, tb := range tables {
		types[tb.Name] = tb.Type
	}
	if types["ed_items"] != "table" || types["ed_view"] != "view" || types["ed weird"] != "table" {
		t.Errorf("tables = %v", types)
	}
	st, err := c.Structure(ctx, "ed_items")
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Columns) != 7 || st.Columns[0].Name != "id" || st.Columns[0].Key != "PRI" || st.Columns[0].Extra != "auto_increment" {
		t.Errorf("columns = %+v", st.Columns)
	}
	if len(st.PrimaryKey) != 1 || st.PrimaryKey[0] != "id" {
		t.Errorf("primary key = %v", st.PrimaryKey)
	}
	if st.Columns[1].Nullable || !st.Columns[2].Nullable {
		t.Errorf("nullability wrong: %+v", st.Columns[1:3])
	}
	var hasUnique bool
	for _, ix := range st.Indexes {
		if ix.Unique && !ix.Primary && len(ix.Columns) == 1 && ix.Columns[0] == "name" {
			hasUnique = true
		}
	}
	if !hasUnique {
		t.Errorf("unique index on name not reported: %+v", st.Indexes)
	}
	cst, err := c.Structure(ctx, "ed_child")
	if err != nil {
		t.Fatal(err)
	}
	if len(cst.ForeignKeys) != 1 || cst.ForeignKeys[0].RefTable != "ed_items" || cst.ForeignKeys[0].Columns[0] != "item_id" || cst.ForeignKeys[0].RefColumns[0] != "id" {
		t.Errorf("foreign keys = %+v", cst.ForeignKeys)
	}
	if _, err := c.Structure(ctx, "nope"); err == nil {
		t.Error("a missing table must be an error")
	}

	// --- insert / browse ---
	for i, name := range []string{"apple", "banana", "cherry", "date", "elder"} {
		vals := map[string]any{"name": name, "price": float64(i) + 0.5, "big": "9007199254740993", "note": name + " note"}
		if i == 2 {
			vals["price"] = nil // NULL
		}
		if n, err := c.Insert(ctx, "ed_items", vals); err != nil || n != 1 {
			t.Fatalf("insert %s: %d %v", name, n, err)
		}
	}
	res, err := c.Browse(ctx, EditorBrowse{Table: "ed_items", PageSize: 2, Page: 2})
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 5 || len(res.Rows) != 2 || res.Rows[0][1] != "cherry" {
		t.Errorf("page 2 of 2/page = total %d rows %v", res.Total, res.Rows)
	}
	if res.Rows[0][2] != nil {
		t.Errorf("NULL price must be nil, got %v", res.Rows[0][2])
	}
	if res.Rows[1][3] != "9007199254740993" {
		t.Errorf("a bigint beyond JS precision must arrive as a string, got %#v", res.Rows[1][3])
	}
	res, err = c.Browse(ctx, EditorBrowse{Table: "ed_items", Sort: "name", Desc: true, Filters: []EditorFilter{{Column: "name", Op: "CONTAINS", Value: "an"}}})
	if err != nil || res.Total != 1 || res.Rows[0][1] != "banana" {
		t.Errorf("filter/sort = %+v %v", res, err)
	}
	res, err = c.Browse(ctx, EditorBrowse{Table: "ed_items", Filters: []EditorFilter{{Column: "price", Op: "IS NULL"}}})
	if err != nil || res.Total != 1 || res.Rows[0][1] != "cherry" {
		t.Errorf("IS NULL filter = %+v %v", res, err)
	}
	res, err = c.Browse(ctx, EditorBrowse{Table: "ed_items", Sort: "id", Desc: true, PageSize: 1})
	if err != nil || res.Rows[0][1] != "elder" {
		t.Fatalf("descending sort = %+v %v", res, err)
	}

	// --- edit one row by primary key ---
	key := map[string]any{"id": res.Rows[0][0]}
	if n, err := c.Update(ctx, "ed_items", key, map[string]any{"name": "elderberry", "note": nil}); err != nil || n != 1 {
		t.Fatalf("update: %d %v", n, err)
	}
	row, err := c.GetRow(ctx, "ed_items", key)
	if err != nil || row["name"] != "elderberry" || row["note"] != nil {
		t.Errorf("row after update = %v %v", row, err)
	}
	if n, err := c.Update(ctx, "ed_items", key, map[string]any{"name": "elderberry"}); err != nil || n != 1 {
		t.Errorf("updating to the same value must still count as found: %d %v", n, err)
	}
	if _, err := c.Update(ctx, "ed_items", map[string]any{"id": 99999}, map[string]any{"name": "x"}); err == nil {
		t.Error("updating a missing row must say so")
	}
	if n, err := c.Delete(ctx, "ed_items", key); err != nil || n != 1 {
		t.Fatalf("delete: %d %v", n, err)
	}
	if _, err := c.Delete(ctx, "ed_items", key); err == nil {
		t.Error("deleting a row twice must say so")
	}

	// --- refusals and injection attempts ---
	if _, err := c.Insert(ctx, "ed_view", map[string]any{"name": "x"}); err == nil {
		t.Error("inserting into a view must be refused")
	}
	if _, err := c.Insert(ctx, "ed_items", map[string]any{"name": "x", "id) VALUES (1); DROP TABLE ed_items; --": "y"}); err == nil {
		t.Error("a hostile column name was accepted")
	}
	if _, err := c.Browse(ctx, EditorBrowse{Table: "ed_items; DROP TABLE ed_items"}); err == nil {
		t.Error("a hostile table name was accepted")
	}
	if _, err := c.Browse(ctx, EditorBrowse{Table: "ed_items", Sort: "id; DROP TABLE ed_items"}); err == nil {
		t.Error("a hostile sort column was accepted")
	}
	// A key that only looks like SQL is just a value: it can match at most the
	// one row it is compared to (MySQL reads "1 OR 1=1" as the number 1), never many.
	_, _ = c.Update(ctx, "ed_items", map[string]any{"id": "1 OR 1=1"}, map[string]any{"name": "pwned"})
	if r, _ := c.Browse(ctx, EditorBrowse{Table: "ed_items", Filters: []EditorFilter{{Column: "name", Op: "=", Value: "pwned"}}}); r != nil && r.Total > 1 {
		t.Errorf("an injected key updated %d rows", r.Total)
	}
	if _, err := c.Update(ctx, "ed_items", map[string]any{"name": "a"}, map[string]any{"name": "b"}); err == nil {
		t.Error("a key that is not the primary key must be refused")
	}
	// A value that looks like SQL is data, not SQL.
	evil := "x'); DROP TABLE ed_items; --"
	if _, err := c.Insert(ctx, "ed_items", map[string]any{"name": evil}); err != nil {
		t.Fatal(err)
	}
	if r, err := c.Browse(ctx, EditorBrowse{Table: "ed_items", Filters: []EditorFilter{{Column: "name", Op: "=", Value: evil}}}); err != nil || r.Total != 1 || r.Rows[0][1] != evil {
		t.Errorf("hostile value was not stored verbatim: %+v %v", r, err)
	}
	// An oddly named table and column round-trip through quoting.
	if n, err := c.Insert(ctx, "ed weird", map[string]any{`we"ird col`: float64(1)}); err != nil || n != 1 {
		t.Errorf("odd table/column names: %d %v", n, err)
	}

	// --- SQL console ---
	q, err := c.Exec(ctx, "SELECT id, name FROM ed_items ORDER BY id; ", 0)
	if err != nil || len(q.Rows) < 4 || len(q.Columns) != 2 {
		t.Errorf("select via console = %+v %v", q, err)
	}
	if q, err := c.Exec(ctx, "UPDATE ed_items SET note = 'console' WHERE name = 'apple'", 0); err != nil || q.Affected != 1 {
		t.Errorf("update via console = %+v %v", q, err)
	}
	if q, err := c.Exec(ctx, "SELECT 1 AS a UNION SELECT 2 UNION SELECT 3", 2); err != nil || len(q.Rows) != 2 || !q.Truncated {
		t.Errorf("row cap = %+v %v", q, err)
	}
	if _, err := c.Exec(ctx, "SELECT 1; SELECT 2", 0); err == nil {
		t.Error("two statements in one run must be refused")
	}
	if _, err := c.Exec(ctx, "   ;  ", 0); err == nil {
		t.Error("an empty statement must be refused")
	}
	if _, err := c.Exec(ctx, "SELEKT nonsense", 0); err == nil {
		t.Error("a syntax error must surface")
	}
	if _, err := c.Exec(ctx, "CREATE TABLE ed_made (a INT)", 0); err != nil {
		t.Errorf("DDL via console: %v", err)
	}
	if err := c.Drop(ctx, "ed_made"); err != nil {
		t.Errorf("drop: %v", err)
	}

	// --- destructive table operations ---
	if err := c.Truncate(ctx, "ed_view"); err == nil {
		t.Error("truncating a view must be refused")
	}
	if err := c.Truncate(ctx, "ed_child"); err != nil {
		t.Errorf("truncate: %v", err)
	}
	if err := c.Drop(ctx, "ed_view"); err != nil {
		t.Errorf("dropping a view: %v", err)
	}
	if err := c.Drop(ctx, "nope"); err == nil {
		t.Error("dropping a missing table must fail")
	}
}
