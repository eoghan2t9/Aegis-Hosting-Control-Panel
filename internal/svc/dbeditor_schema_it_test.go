package svc

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestSchemaValidation(t *testing.T) {
	for _, kind := range []string{"mariadb", "postgres"} {
		c := &EditorConn{d: mysqlDialect}
		strT, intT, tsT := "varchar", "int", "timestamp"
		if kind == "postgres" {
			c.d = pgDialect
			strT, intT, tsT = "character varying", "integer", "timestamp without time zone"
		}
		good := SchemaColumn{Name: "a b", Type: strT, Length: "20", Nullable: true, Default: "value", DefaultValue: "it's"}
		ddl, err := c.columnDDL(&good)
		if err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		if !strings.Contains(ddl, "DEFAULT 'it''s'") {
			t.Errorf("%s: default not escaped: %s", kind, ddl)
		}
		bad := []SchemaColumn{
			{Name: "x", Type: strT + "; DROP TABLE x"},
			{Name: "x", Type: strT, Length: "20)); DROP TABLE t; --"},
			{Name: "x`y", Type: strT, Length: "5"},
			{Name: `x"y`, Type: strT, Length: "5"},
			{Name: "", Type: strT, Length: "5"},
			{Name: strings.Repeat("a", 65), Type: strT, Length: "5"},
			{Name: "x", Type: strT, Length: "5", AutoIncrement: true},
			{Name: "x", Type: intT, Default: "value", DefaultValue: "1; DROP TABLE t"},
			{Name: "x", Type: intT, Default: "expr", DefaultValue: "now()"},
			{Name: "x", Type: intT, Default: "null"}, // NOT NULL cannot default to NULL
			{Name: "x", Type: intT, Default: "current_timestamp", Nullable: true},
			{Name: "x", Type: tsT, Length: "9"}, // fractional seconds are 0-6
		}
		for i, b := range bad {
			b := b
			if out, err := c.columnDDL(&b); err == nil {
				t.Errorf("%s: case %d should be refused, got %s", kind, i, out)
			}
		}
		stamp := SchemaColumn{Name: "made", Type: tsT, Nullable: true, Default: "current_timestamp"}
		if out, err := c.columnDDL(&stamp); err != nil || !strings.Contains(out, "DEFAULT CURRENT_TIMESTAMP") {
			t.Errorf("%s: timestamp default: %q %v", kind, out, err)
		}
	}
	c := &EditorConn{d: mysqlDialect}
	enum := SchemaColumn{Name: "e", Type: "enum", Values: []string{"a", "b'c", `d\e`}}
	if ddl, err := c.columnDDL(&enum); err != nil || !strings.Contains(ddl, `enum('a', 'b''c', 'd\\e')`) {
		t.Errorf("enum ddl = %q %v", ddl, err)
	}
	un := SchemaColumn{Name: "n", Type: "int", Unsigned: true, AutoIncrement: true}
	if ddl, err := c.columnDDL(&un); err != nil || ddl != "`n` int unsigned NOT NULL AUTO_INCREMENT" {
		t.Errorf("auto ddl = %q %v", ddl, err)
	}
	if err := validNewName("x", "  pad"); err == nil {
		t.Error("leading space must be refused")
	}
	if len(SchemaTypes("mariadb")) != len(mysqlTypes) || len(SchemaTypes("postgres")) != len(pgTypes) {
		t.Error("the offered type list must cover every allowed type")
	}
}

func TestEditorSchemaMySQL(t *testing.T)    { runSchemaIT(t, itConn(t, "mysql")) }
func TestEditorSchemaPostgres(t *testing.T) { runSchemaIT(t, itConn(t, "pg")) }

func runSchemaIT(t *testing.T, c *EditorConn) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pg := c.d.kind == "postgres"
	setupIT(t, c) // ed_items, ed_child, "ed weird", ed_view
	cleanup := func() {
		for _, n := range []string{"ed_new", "ed_new2", "ed_copy", "ed_ren"} {
			_, _ = c.db.ExecContext(context.Background(), "DROP TABLE IF EXISTS "+c.d.quote(n))
		}
	}
	cleanup()
	t.Cleanup(cleanup)

	apply := func(req SchemaRequest) *SchemaResult {
		t.Helper()
		res, err := c.ApplySchema(ctx, req, false, nil)
		if err != nil {
			t.Fatalf("%s %s: %v (%+v)", req.Action, req.Table, err, res)
		}
		return res
	}
	refuse := func(req SchemaRequest, why string) {
		t.Helper()
		if _, err := c.ApplySchema(ctx, req, false, nil); err == nil {
			t.Errorf("%s should have been refused: %s", req.Action, why)
		}
	}
	strT, intT, tsT, decT := "varchar", "int", "timestamp", "decimal"
	if pg {
		strT, intT, tsT, decT = "character varying", "integer", "timestamp without time zone", "numeric"
	}

	// --- create table ---
	create := SchemaRequest{Action: "create_table", Table: "ed_new", KeyColumns: []string{"id"}, Columns: []SchemaColumn{
		{Name: "id", Type: intT, AutoIncrement: true},
		{Name: "title", Type: strT, Length: "40", Default: "value", DefaultValue: "untitled"},
		{Name: "qty", Type: intT, Nullable: true},
		{Name: "made", Type: tsT, Nullable: true, Default: "current_timestamp"},
	}}
	pre, err := c.ApplySchema(ctx, create, true, nil)
	if err != nil || pre.Applied || len(pre.Statements) != 1 {
		t.Fatalf("preview: %+v %v", pre, err)
	}
	if tables, _ := c.Tables(ctx); hasTable(tables, "ed_new") {
		t.Fatal("a preview must not create the table")
	}
	apply(create)
	st, err := c.Structure(ctx, "ed_new")
	if err != nil || len(st.Columns) != 4 || len(st.PrimaryKey) != 1 || st.Columns[0].Extra != "auto_increment" {
		t.Fatalf("created structure: %+v %v", st, err)
	}
	if _, err := c.Insert(ctx, "ed_new", map[string]any{"qty": int64(3)}); err != nil {
		t.Fatalf("insert into the new table (default/auto-increment): %v", err)
	}
	refuse(create, "table already exists")
	refuse(SchemaRequest{Action: "create_table", Table: "ed_bad", Columns: []SchemaColumn{{Name: "a", Type: "nope"}}}, "unknown type")
	refuse(SchemaRequest{Action: "create_table", Table: "ed_bad", KeyColumns: []string{"zz"}, Columns: []SchemaColumn{{Name: "a", Type: intT}}}, "key column missing")
	if !pg {
		refuse(SchemaRequest{Action: "create_table", Table: "ed_bad", Columns: []SchemaColumn{{Name: "a", Type: intT, AutoIncrement: true}}}, "auto-increment outside the key")
	}

	// --- columns ---
	apply(SchemaRequest{Action: "add_column", Table: "ed_new", Column: &SchemaColumn{Name: "note", Type: "text", Nullable: true, Comment: "free text"}})
	apply(SchemaRequest{Action: "add_column", Table: "ed_new", Column: &SchemaColumn{Name: "price", Type: decT, Length: "8,2", Nullable: true}, After: "title"})
	refuse(SchemaRequest{Action: "add_column", Table: "ed_new", Column: &SchemaColumn{Name: "note", Type: "text"}}, "duplicate column")
	refuse(SchemaRequest{Action: "add_column", Table: "ed_view", Column: &SchemaColumn{Name: "x", Type: "text"}}, "a view")

	apply(SchemaRequest{Action: "modify_column", Table: "ed_new", Name: "title", Column: &SchemaColumn{Name: "heading", Type: strT, Length: "80", Default: "value", DefaultValue: "none yet"}})
	st, _ = c.Structure(ctx, "ed_new")
	var heading *EditorColumn
	for i := range st.Columns {
		if st.Columns[i].Name == "heading" {
			heading = &st.Columns[i]
		}
	}
	if heading == nil || !strings.Contains(heading.Type, "80") || heading.Default == nil || !strings.Contains(*heading.Default, "none yet") {
		t.Errorf("modified column: %+v", heading)
	}
	if pg {
		apply(SchemaRequest{Action: "modify_column", Table: "ed_new", Name: "qty", Column: &SchemaColumn{Name: "qty", Type: "bigint", Nullable: false, Default: "value", DefaultValue: "0"}})
		st, _ = c.Structure(ctx, "ed_new")
		for _, col := range st.Columns {
			if col.Name == "qty" && (col.Type != "bigint" || col.Nullable) {
				t.Errorf("qty after modify: %+v", col)
			}
		}
		refuse(SchemaRequest{Action: "modify_column", Table: "ed_new", Name: "qty", Column: &SchemaColumn{Name: "qty", Type: "bigint", AutoIncrement: true}}, "toggling auto-increment")
		refuse(SchemaRequest{Action: "modify_column", Table: "ed_new", Name: "qty", Column: &SchemaColumn{Name: "qty", Type: "bigint", Default: "value", DefaultValue: "0"}}, "nothing to change")
	}
	apply(SchemaRequest{Action: "drop_column", Table: "ed_new", Name: "note"})
	refuse(SchemaRequest{Action: "drop_column", Table: "ed_new", Name: "note"}, "already dropped")

	// --- indexes and foreign keys ---
	apply(SchemaRequest{Action: "add_index", Table: "ed_new", Name: "ix_heading", KeyColumns: []string{"heading"}})
	apply(SchemaRequest{Action: "add_index", Table: "ed_new", Name: "uq_qty_heading", KeyColumns: []string{"qty", "heading"}, Unique: true})
	refuse(SchemaRequest{Action: "add_index", Table: "ed_new", Name: "ix_heading", KeyColumns: []string{"heading"}}, "duplicate index name")
	refuse(SchemaRequest{Action: "add_index", Table: "ed_new", Name: "ix_bad", KeyColumns: []string{"missing"}}, "missing column")
	st, _ = c.Structure(ctx, "ed_new")
	if !hasIndex(st, "ix_heading") || !hasIndex(st, "uq_qty_heading") {
		t.Errorf("indexes: %+v", st.Indexes)
	}
	apply(SchemaRequest{Action: "drop_index", Table: "ed_new", Name: "ix_heading"})
	refuse(SchemaRequest{Action: "drop_index", Table: "ed_new", Name: "PRIMARY"}, "primary key")
	refuse(SchemaRequest{Action: "drop_index", Table: "ed_new", Name: "ix_heading"}, "already dropped")

	if _, err := c.Insert(ctx, "ed_items", map[string]any{"id": int64(1), "name": "fk parent"}); err != nil {
		t.Fatal(err)
	}
	apply(SchemaRequest{Action: "add_foreign_key", Table: "ed_new", Name: "fk_new_item", KeyColumns: []string{"id"}, RefTable: "ed_items", RefColumns: []string{"id"}, OnDelete: "cascade", OnUpdate: "restrict"})
	st, _ = c.Structure(ctx, "ed_new")
	if len(st.ForeignKeys) != 1 || st.ForeignKeys[0].RefTable != "ed_items" {
		t.Errorf("foreign keys: %+v", st.ForeignKeys)
	}
	refuse(SchemaRequest{Action: "add_foreign_key", Table: "ed_new", Name: "fk_x", KeyColumns: []string{"id"}, RefTable: "ed_items", RefColumns: []string{"id"}, OnDelete: "DROP TABLE x"}, "bad action")
	refuse(SchemaRequest{Action: "add_foreign_key", Table: "ed_new", Name: "fk_x", KeyColumns: []string{"id"}, RefTable: "ed_view", RefColumns: []string{"id"}}, "referencing a view")
	refuse(SchemaRequest{Action: "add_foreign_key", Table: "ed_new", Name: "fk_x", KeyColumns: []string{"id", "qty"}, RefTable: "ed_items", RefColumns: []string{"id"}}, "column count mismatch")
	apply(SchemaRequest{Action: "drop_foreign_key", Table: "ed_new", Name: "fk_new_item"})
	refuse(SchemaRequest{Action: "drop_foreign_key", Table: "ed_new", Name: "fk_new_item"}, "already dropped")

	// --- copy / rename ---
	apply(SchemaRequest{Action: "copy_table", Table: "ed_new", NewName: "ed_copy", Data: true})
	if q, err := c.Exec(ctx, "SELECT COUNT(*) FROM ed_copy", 0); err != nil || len(q.Rows) != 1 || q.Rows[0][0] != int64(1) && q.Rows[0][0] != "1" {
		t.Errorf("copied rows: %+v %v", q, err)
	}
	refuse(SchemaRequest{Action: "copy_table", Table: "ed_new", NewName: "ed_copy"}, "target exists")
	apply(SchemaRequest{Action: "rename_table", Table: "ed_copy", NewName: "ed_ren"})
	refuse(SchemaRequest{Action: "rename_table", Table: "ed_ren", NewName: "ed_items"}, "target exists")
	refuse(SchemaRequest{Action: "rename_table", Table: "ed_ren", NewName: "bad`name"}, "bad name")
	refuse(SchemaRequest{Action: "nonsense", Table: "ed_ren"}, "unknown action")
	refuse(SchemaRequest{Action: "drop_column", Table: "no_such", Name: "x"}, "missing table")

	// --- maintenance ---
	ops := []string{"analyze", "optimize", "check"}
	if pg {
		ops = []string{"analyze", "vacuum", "reindex"}
	}
	for _, op := range ops {
		if _, _, err := c.Maintenance(ctx, "ed_new", op); err != nil {
			t.Errorf("maintenance %s: %v", op, err)
		}
	}
	if _, _, err := c.Maintenance(ctx, "ed_new", "drop"); err == nil {
		t.Error("an unknown maintenance op must be refused")
	}
	if _, _, err := c.Maintenance(ctx, "ed_view", "analyze"); err == nil {
		t.Error("maintenance on a view must be refused")
	}

	// --- search ---
	if _, err := c.Insert(ctx, "ed_items", map[string]any{"id": int64(99), "name": "Needle_50%"}); err != nil {
		t.Fatal(err)
	}
	sr, err := c.Search(ctx, "needle_50%", nil)
	if err != nil || len(sr.Hits) != 1 || sr.Hits[0].Table != "ed_items" || len(sr.Hits[0].Rows) != 1 {
		t.Errorf("search: %+v %v", sr, err)
	}
	if sr, err := c.Search(ctx, "no-such-value-anywhere", nil); err != nil || len(sr.Hits) != 0 || sr.Searched == 0 {
		t.Errorf("empty search: %+v %v", sr, err)
	}
	if _, err := c.Search(ctx, "x", []string{"nope"}); err == nil {
		t.Error("searching a missing table must be refused")
	}
	if _, err := c.Search(ctx, "  ", nil); err == nil {
		t.Error("an empty search must be refused")
	}
	if sr, err := c.Search(ctx, "1'; DROP TABLE ed_items; --", nil); err != nil || len(sr.Hits) != 0 {
		t.Errorf("injection-shaped search: %+v %v", sr, err)
	}
	if _, err := c.Structure(ctx, "ed_items"); err != nil {
		t.Errorf("ed_items must survive the injection attempt: %v", err)
	}

	// --- routines and triggers ---
	setup := []string{
		"DROP TRIGGER IF EXISTS ed_trg", "DROP FUNCTION IF EXISTS ed_fn",
		"CREATE FUNCTION ed_fn() RETURNS INT DETERMINISTIC RETURN 1",
		"CREATE TRIGGER ed_trg BEFORE INSERT ON ed_new FOR EACH ROW SET NEW.qty = 5",
	}
	if pg {
		setup = []string{
			"DROP FUNCTION IF EXISTS ed_fn() CASCADE",
			"CREATE FUNCTION ed_fn() RETURNS trigger AS $$ BEGIN RETURN NEW; END; $$ LANGUAGE plpgsql",
			"CREATE TRIGGER ed_trg BEFORE INSERT ON ed_new FOR EACH ROW EXECUTE FUNCTION ed_fn()",
		}
	}
	for _, s := range setup {
		if _, err := c.db.ExecContext(ctx, s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	t.Cleanup(func() {
		drop := "DROP FUNCTION IF EXISTS ed_fn"
		if pg {
			drop += "() CASCADE"
		}
		_, _ = c.db.ExecContext(context.Background(), drop)
	})
	objs, err := c.Objects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var sawFn, sawTrg bool
	for _, o := range objs {
		if o.Name == "ed_fn" && o.Kind == "function" && strings.Contains(strings.ToLower(o.Definition), "ed_fn") {
			sawFn = true
		}
		if o.Name == "ed_trg" && o.Kind == "trigger" && o.Table == "ed_new" && o.Definition != "" {
			sawTrg = true
		}
	}
	if !sawFn || !sawTrg {
		t.Errorf("objects: fn=%v trigger=%v in %+v", sawFn, sawTrg, objs)
	}
}

func hasTable(ts []EditorTable, name string) bool {
	for _, t := range ts {
		if t.Name == name {
			return true
		}
	}
	return false
}

func hasIndex(st *EditorStructure, name string) bool {
	for _, ix := range st.Indexes {
		if ix.Name == name {
			return true
		}
	}
	return false
}
