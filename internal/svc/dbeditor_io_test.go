package svc

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestSplitStatementsMySQL(t *testing.T) {
	cases := []struct {
		name, in string
		want     []string
	}{
		{"two", "SELECT 1; SELECT 2;", []string{"SELECT 1", "SELECT 2"}},
		{"no trailing semicolon", "SELECT 1", []string{"SELECT 1"}},
		{"empty statements skipped", ";;  ;\nSELECT 1;;", []string{"SELECT 1"}},
		{"semicolon in string", "INSERT INTO t VALUES ('a;b'); SELECT 2", []string{"INSERT INTO t VALUES ('a;b')", "SELECT 2"}},
		{"escaped quote", `INSERT INTO t VALUES ('it\'s; ok'); SELECT 2`, []string{`INSERT INTO t VALUES ('it\'s; ok')`, "SELECT 2"}},
		{"doubled quote", "INSERT INTO t VALUES ('it''s; ok'); SELECT 2", []string{"INSERT INTO t VALUES ('it''s; ok')", "SELECT 2"}},
		{"backtick ident", "SELECT `a;b` FROM t; SELECT 2", []string{"SELECT `a;b` FROM t", "SELECT 2"}},
		{"line comment dash", "SELECT 1; -- drop;\nSELECT 2", []string{"SELECT 1", "SELECT 2"}},
		{"line comment hash", "# hi; there\nSELECT 1; SELECT 2", []string{"SELECT 1", "SELECT 2"}},
		{"block comment", "SELECT /* a; b */ 1; SELECT 2", []string{"SELECT 1", "SELECT 2"}},
		{"executable comment kept", "/*!40101 SET NAMES utf8 */; SELECT 2", []string{"/*!40101 SET NAMES utf8 */", "SELECT 2"}},
		{"dash without space is not a comment", "SELECT 5--3; SELECT 2", []string{"SELECT 5--3", "SELECT 2"}},
		{"delimiter", "DELIMITER $$\nCREATE PROCEDURE p() BEGIN SELECT 1; SELECT 2; END$$\nDELIMITER ;\nSELECT 3;",
			[]string{"CREATE PROCEDURE p() BEGIN SELECT 1; SELECT 2; END", "SELECT 3"}},
		{"unterminated string", "SELECT 'abc; SELECT 2", []string{"SELECT 'abc; SELECT 2"}},
	}
	for _, c := range cases {
		got := splitStatements(c.in, "mariadb")
		for i := range got {
			got[i] = strings.Join(strings.Fields(got[i]), " ")
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestSplitStatementsPostgres(t *testing.T) {
	cases := []struct {
		name, in string
		want     []string
	}{
		{"dollar body", "CREATE FUNCTION f() RETURNS int AS $$ BEGIN RETURN 1; END; $$ LANGUAGE plpgsql; SELECT 2;",
			[]string{"CREATE FUNCTION f() RETURNS int AS $$ BEGIN RETURN 1; END; $$ LANGUAGE plpgsql", "SELECT 2"}},
		{"tagged dollar", "SELECT $tag$a;b$tag$; SELECT 2", []string{"SELECT $tag$a;b$tag$", "SELECT 2"}},
		{"positional param is not a quote", "SELECT $1; SELECT 2", []string{"SELECT $1", "SELECT 2"}},
		{"backslash is literal", `SELECT 'a\'; SELECT 2`, []string{`SELECT 'a\'`, "SELECT 2"}},
		{"hash is not a comment", "SELECT 1 # 2; SELECT 3", []string{"SELECT 1 # 2", "SELECT 3"}},
		{"dash comment without space", "SELECT 1; --note\nSELECT 2", []string{"SELECT 1", "SELECT 2"}},
		{"double quoted ident", `SELECT "a;b"; SELECT 2`, []string{`SELECT "a;b"`, "SELECT 2"}},
	}
	for _, c := range cases {
		got := splitStatements(c.in, "postgres")
		for i := range got {
			got[i] = strings.Join(strings.Fields(got[i]), " ")
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestSQLLiteral(t *testing.T) {
	cases := []struct {
		d    dialect
		v    any
		typ  string
		want string
	}{
		{mysqlDialect, nil, "", "NULL"},
		{mysqlDialect, "it's", "VARCHAR", `'it''s'`},
		{mysqlDialect, `a\b`, "VARCHAR", `'a\\b'`},
		{pgDialect, `a\b`, "TEXT", `'a\b'`},
		{mysqlDialect, "a\x00b", "VARCHAR", `'a\0b'`},
		{mysqlDialect, int64(-5), "INT", "-5"},
		{mysqlDialect, true, "BOOL", "1"},
		{pgDialect, true, "BOOL", "TRUE"},
		{mysqlDialect, []byte{0xde, 0xad}, "BLOB", "X'dead'"},
		{pgDialect, []byte{0xde, 0xad}, "BYTEA", `'\xdead'::bytea`},
		{mysqlDialect, []byte("text"), "VARCHAR", "'text'"},
		{mysqlDialect, time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC), "DATE", "'2026-01-02'"},
	}
	for _, c := range cases {
		if got := sqlLiteral(c.d, c.v, c.typ); got != c.want {
			t.Errorf("sqlLiteral(%s, %#v, %s) = %s, want %s", c.d.kind, c.v, c.typ, got, c.want)
		}
	}
}

func TestPGCreateStatements(t *testing.T) {
	def := "'x'::text"
	st := &EditorStructure{
		Table: "we\"ird",
		Columns: []EditorColumn{
			{Name: "id", Type: "integer", Extra: "auto_increment"},
			{Name: "name", Type: "character varying(50)", Default: &def},
			{Name: "note", Type: "text", Nullable: true},
		},
		PrimaryKey: []string{"id"},
		Indexes: []EditorIndex{
			{Name: "t_pkey", Columns: []string{"id"}, Unique: true, Primary: true, Method: "btree"},
			{Name: "t_name", Columns: []string{"name"}, Unique: true, Method: "btree"},
			{Name: "t_note", Columns: []string{"note"}, Method: "gin"},
		},
	}
	got := pgCreateStatements(st)
	if len(got) != 3 {
		t.Fatalf("got %d statements: %q", len(got), got)
	}
	for _, want := range []string{`"we""ird"`, `"id" SERIAL`, `"name" character varying(50) NOT NULL DEFAULT 'x'::text`, `PRIMARY KEY ("id")`} {
		if !strings.Contains(got[0], want) {
			t.Errorf("CREATE TABLE missing %q:\n%s", want, got[0])
		}
	}
	if strings.Contains(got[0], "nextval") {
		t.Errorf("serial column must not keep its nextval default:\n%s", got[0])
	}
	if got[1] != `CREATE UNIQUE INDEX "t_name" ON "we""ird" ("name")` {
		t.Errorf("index 1 = %s", got[1])
	}
	if got[2] != `CREATE INDEX "t_note" ON "we""ird" USING gin ("note")` {
		t.Errorf("index 2 = %s", got[2])
	}
}

func TestCSVValue(t *testing.T) {
	if csvValue(nil, "", `\N`) != `\N` || csvValue("", "", `\N`) != "" {
		t.Error("NULL and empty string must stay distinct")
	}
	if got := csvValue([]byte{1, 2}, "BLOB", `\N`); got != "0x0102" {
		t.Errorf("binary = %q", got)
	}
}
