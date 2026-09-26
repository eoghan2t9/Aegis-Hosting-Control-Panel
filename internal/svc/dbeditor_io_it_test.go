package svc

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

func TestEditorIOMySQL(t *testing.T)    { runEditorIOIT(t, itConn(t, "mysql")) }
func TestEditorIOPostgres(t *testing.T) { runEditorIOIT(t, itConn(t, "pg")) }

func csvOf(t *testing.T, c *EditorConn, table string) string {
	t.Helper()
	var b bytes.Buffer
	if err := c.ExportCSV(context.Background(), &b, table, true, ""); err != nil {
		t.Fatalf("export csv %s: %v", table, err)
	}
	return b.String()
}

func runEditorIOIT(t *testing.T, c *EditorConn) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	setupIT(t, c)

	// Awkward values: quotes, backslashes, semicolons, unicode, NULL, binary, big integers.
	nasty := []string{"plain", "it's a \"test\"", `back\slash\'x`, "semi;colon -- not a comment", "line\nbreak\ttab", "emoji ✓ ünï", ""}
	for i, name := range nasty {
		vals := map[string]any{"name": name, "big": "9007199254740993", "note": name}
		if i == 2 {
			vals["note"] = nil
		}
		if _, err := c.Insert(ctx, "ed_items", vals); err != nil {
			t.Fatalf("insert %q: %v", name, err)
		}
	}
	if _, err := c.Exec(ctx, "INSERT INTO ed_child (id, item_id) VALUES (1, 1), (2, 2)", 0); err != nil {
		t.Fatal(err)
	}
	wantItems := csvOf(t, c, "ed_items")
	wantChild := csvOf(t, c, "ed_child")

	// --- SQL export -> wipe -> import -> identical ---
	var dump bytes.Buffer
	if err := c.ExportSQL(ctx, &dump, "it", ExportOptions{Structure: true, Data: true, DropIfExists: true}); err != nil {
		t.Fatalf("export: %v", err)
	}
	text := dump.String()
	for _, want := range []string{"ed_items", "ed_child", "ed_view", "ed weird", "INSERT INTO"} {
		if !strings.Contains(text, want) {
			t.Errorf("dump is missing %q", want)
		}
	}
	for _, q := range []string{"DROP VIEW ed_view", "DROP TABLE ed_child", "DROP TABLE ed_items", "DROP TABLE " + c.d.quote("ed weird")} {
		if _, err := c.db.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	res, err := c.ImportSQL(ctx, text)
	if err != nil {
		t.Fatalf("import: %v\nresult: %+v\n%s", err, res, text)
	}
	if got := csvOf(t, c, "ed_items"); got != wantItems {
		t.Errorf("ed_items differs after SQL round trip:\nwant %q\ngot  %q", wantItems, got)
	}
	if got := csvOf(t, c, "ed_child"); got != wantChild {
		t.Errorf("ed_child differs after SQL round trip:\nwant %q\ngot  %q", wantChild, got)
	}
	st, err := c.Structure(ctx, "ed_child")
	if err != nil || len(st.ForeignKeys) != 1 {
		t.Errorf("foreign key lost in the round trip: %+v %v", st, err)
	}
	if v, err := c.Structure(ctx, "ed_view"); err != nil || v.Type != "view" {
		t.Errorf("view lost in the round trip: %+v %v", v, err)
	}
	// The serial/auto-increment counter must continue after the imported rows.
	if _, err := c.Insert(ctx, "ed_items", map[string]any{"name": "after import"}); err != nil {
		t.Errorf("insert after import (sequence not advanced?): %v", err)
	}

	// --- a failing import: PostgreSQL rolls everything back ---
	before := csvOf(t, c, "ed_items")
	res, err = c.ImportSQL(ctx, "INSERT INTO ed_items (name) VALUES ('zzz-one'); INSERT INTO ed_items (name) VALUES (NULL); INSERT INTO ed_items (name) VALUES ('zzz-two')")
	if err == nil || res.FailedAt != 2 {
		t.Fatalf("expected statement 2 to fail, got %+v %v", res, err)
	}
	if c.d.kind == "postgres" && csvOf(t, c, "ed_items") != before {
		t.Error("a failed PostgreSQL import must roll back")
	}
	if _, err := c.ImportSQL(ctx, "  -- nothing here\n "); err == nil {
		t.Error("an empty script must be refused")
	}

	// --- CSV export -> truncate -> import ---
	if _, err := c.Exec(ctx, "DELETE FROM ed_child", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Exec(ctx, "DELETE FROM ed_items", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Insert(ctx, "ed_items", map[string]any{"id": int64(7), "name": "seven", "note": nil}); err != nil {
		t.Fatal(err)
	}
	src := csvOf(t, c, "ed_items")
	if _, err := c.Exec(ctx, "DELETE FROM ed_items", 0); err != nil {
		t.Fatal(err)
	}
	cres, err := c.ImportCSV(ctx, strings.NewReader(src), "ed_items", CSVImportOptions{Header: true})
	if err != nil || cres.Rows != 1 {
		t.Fatalf("csv import: %+v %v", cres, err)
	}
	if got := csvOf(t, c, "ed_items"); got != src {
		t.Errorf("csv round trip differs:\nwant %q\ngot  %q", src, got)
	}
	// NULL vs empty string survive.
	if _, err := c.ImportCSV(ctx, strings.NewReader("name,note\nempty,\nnull,\\N\n"), "ed_items", CSVImportOptions{Header: true}); err != nil {
		t.Fatalf("csv import 2: %v", err)
	}
	q, err := c.Exec(ctx, "SELECT name, note FROM ed_items WHERE name IN ('empty','null') ORDER BY name", 0)
	if err != nil || len(q.Rows) != 2 || q.Rows[0][1] != "" || q.Rows[1][1] != nil {
		t.Errorf("empty/NULL handling: %+v %v", q, err)
	}
	// Bad CSV: unknown column, wrong field count, and a DB error all leave nothing behind.
	rowsBefore := csvOf(t, c, "ed_items")
	for name, in := range map[string]string{
		"unknown column": "nope\n1\n",
		"short row":      "name,note\nx\n",
		"db error":       "name\nfresh-row\nseven\n", // the second row violates the unique key
	} {
		if _, err := c.ImportCSV(ctx, strings.NewReader(in), "ed_items", CSVImportOptions{Header: true}); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	if csvOf(t, c, "ed_items") != rowsBefore {
		t.Error("a failed CSV import must not leave rows behind")
	}
	if _, err := c.ImportCSV(ctx, strings.NewReader("a\n1\n"), "ed_view", CSVImportOptions{Header: true}); err == nil {
		t.Error("importing into a view must be refused")
	}
	if _, err := c.ImportCSV(ctx, strings.NewReader("a\n1\n"), "no_such", CSVImportOptions{Header: true}); err == nil {
		t.Error("importing into a missing table must be refused")
	}

	// --- definitions ---
	def, err := c.Definition(ctx, "ed_items")
	if err != nil || !strings.Contains(strings.ToUpper(def), "CREATE TABLE") {
		t.Errorf("definition: %q %v", def, err)
	}

	// --- multi-statement scripts ---
	out, err := c.ExecScript(ctx, "CREATE TABLE ed_made (a INT); INSERT INTO ed_made VALUES (1),(2); SELECT COUNT(*) AS n FROM ed_made -- done", 0, nil)
	if err != nil || len(out) != 3 || out[2].Result == nil || len(out[2].Result.Rows) != 1 {
		t.Fatalf("script: %+v %v", out, err)
	}
	if out[1].Result.Affected != 2 {
		t.Errorf("insert affected = %d", out[1].Result.Affected)
	}
	out, err = c.ExecScript(ctx, "SELECT 1; SELEKT 2; SELECT 3", 0, nil)
	if err == nil || len(out) != 2 || out[1].Error == "" {
		t.Errorf("a script must stop at the first error: %+v %v", out, err)
	}
	_ = c.Drop(ctx, "ed_made")
}
