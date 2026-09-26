package svc

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Database-wide search and read-only listing of routines, triggers and events.

const (
	editorSearchMaxTables  = 300
	editorSearchRowsPerTbl = 10
	editorSearchMaxHits    = 100 // stop after this many matching rows overall
	editorMaxObjects       = 300
)

type SearchHit struct {
	Table     string               `json:"table"`
	Columns   []EditorResultColumn `json:"columns"`
	Rows      [][]any              `json:"rows"`
	Truncated bool                 `json:"truncated,omitempty"` // more matching rows exist than are shown
}

type SearchResult struct {
	Hits      []SearchHit `json:"hits"`
	Searched  int         `json:"searched"`  // tables examined
	Truncated bool        `json:"truncated"` // stopped early (table or hit limit)
}

// Search finds rows whose text form contains term, in every (or the chosen)
// table. Values are compared as text, case-insensitively where the server is,
// and the term is bound as a parameter, never pasted into SQL.
func (c *EditorConn) Search(ctx context.Context, term string, only []string) (*SearchResult, error) {
	term = strings.TrimSpace(term)
	if term == "" {
		return nil, errors.New("enter something to search for")
	}
	if len(term) > 200 {
		return nil, errors.New("the search text is too long")
	}
	all, err := c.Tables(ctx)
	if err != nil {
		return nil, err
	}
	exists := map[string]bool{}
	for _, t := range all {
		exists[t.Name] = true
	}
	want := map[string]bool{}
	for _, n := range only {
		if !exists[n] {
			return nil, fmt.Errorf("table %q does not exist", n)
		}
		want[n] = true
	}
	var names []string
	res := &SearchResult{Hits: []SearchHit{}}
	for _, t := range all {
		if t.Type != "table" || (len(want) > 0 && !want[t.Name]) {
			continue
		}
		if len(names) >= editorSearchMaxTables {
			res.Truncated = true
			break
		}
		names = append(names, t.Name)
	}

	// One catalog query for every searchable column.
	var q string
	if c.d.kind == "postgres" {
		q = `SELECT table_name, column_name FROM information_schema.columns
			WHERE table_schema = 'public' AND data_type <> 'bytea' ORDER BY table_name, ordinal_position`
	} else {
		q = `SELECT TABLE_NAME, COLUMN_NAME FROM information_schema.COLUMNS
			WHERE TABLE_SCHEMA = DATABASE()
			  AND DATA_TYPE NOT IN ('tinyblob','blob','mediumblob','longblob','binary','varbinary','bit')
			ORDER BY TABLE_NAME, ORDINAL_POSITION`
	}
	rows, err := c.db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	colsOf := map[string][]string{}
	for rows.Next() {
		var t, col string
		if err := rows.Scan(&t, &col); err != nil {
			rows.Close()
			return nil, err
		}
		colsOf[t] = append(colsOf[t], col)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	pattern := "%" + escapeLike(term) + "%"
	op, castTo := "LIKE", "CHAR"
	if c.d.kind == "postgres" {
		op, castTo = "ILIKE", "TEXT"
	}
	total := 0
	for _, t := range names {
		if ctx.Err() != nil || total >= editorSearchMaxHits {
			res.Truncated = true
			break
		}
		cols := colsOf[t]
		if len(cols) == 0 {
			continue
		}
		if len(cols) > 200 {
			cols = cols[:200]
		}
		conds := make([]string, len(cols))
		args := make([]any, len(cols))
		for i, col := range cols {
			conds[i] = "CAST(" + c.d.quote(col) + " AS " + castTo + ") " + op + " " + c.d.ph(i+1)
			args[i] = pattern
		}
		res.Searched++
		r, err := c.db.QueryContext(ctx, "SELECT * FROM "+c.d.quote(t)+" WHERE "+strings.Join(conds, " OR ")+
			fmt.Sprintf(" LIMIT %d", editorSearchRowsPerTbl+1), args...)
		if err != nil {
			continue // an unreadable table or a type that cannot be cast: skip it, keep searching
		}
		out, err := scanRows(r, editorSearchRowsPerTbl, editorMaxBytes)
		r.Close()
		if err != nil || len(out.Rows) == 0 {
			continue
		}
		total += len(out.Rows)
		res.Hits = append(res.Hits, SearchHit{Table: t, Columns: out.Columns, Rows: out.Rows, Truncated: out.Truncated})
	}
	return res, nil
}

// EditorObject is a stored routine, trigger or event, shown read-only.
type EditorObject struct {
	Kind       string `json:"kind"` // procedure, function, trigger or event
	Name       string `json:"name"`
	Table      string `json:"table,omitempty"` // triggers
	Definition string `json:"definition"`
}

// Objects lists the database's routines, triggers and (MariaDB) events with
// their definitions. Definitions the user has no privilege to read are empty.
func (c *EditorConn) Objects(ctx context.Context) ([]EditorObject, error) {
	if c.d.kind == "postgres" {
		return c.objectsPG(ctx)
	}
	return c.objectsMySQL(ctx)
}

func (c *EditorConn) objectsPG(ctx context.Context) ([]EditorObject, error) {
	out := []EditorObject{}
	rows, err := c.db.QueryContext(ctx, fmt.Sprintf(`SELECT CASE p.prokind WHEN 'p' THEN 'procedure' ELSE 'function' END, p.proname, pg_get_functiondef(p.oid)
		FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
		WHERE n.nspname = 'public' AND p.prokind IN ('f','p')
		  AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.objid = p.oid AND d.deptype = 'e')
		ORDER BY p.proname LIMIT %d`, editorMaxObjects))
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var o EditorObject
		if err := rows.Scan(&o.Kind, &o.Name, &o.Definition); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, o)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	trows, err := c.db.QueryContext(ctx, fmt.Sprintf(`SELECT t.tgname, c.relname, pg_get_triggerdef(t.oid)
		FROM pg_trigger t JOIN pg_class c ON c.oid = t.tgrelid JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'public' AND NOT t.tgisinternal ORDER BY c.relname, t.tgname LIMIT %d`, editorMaxObjects))
	if err != nil {
		return nil, err
	}
	defer trows.Close()
	for trows.Next() {
		o := EditorObject{Kind: "trigger"}
		if err := trows.Scan(&o.Name, &o.Table, &o.Definition); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, trows.Err()
}

func (c *EditorConn) objectsMySQL(ctx context.Context) ([]EditorObject, error) {
	out := []EditorObject{}
	list := func(q string, mk func(a, b string) EditorObject) error {
		rows, err := c.db.QueryContext(ctx, q)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var a, b string
			if err := rows.Scan(&a, &b); err != nil {
				return err
			}
			if len(out) < editorMaxObjects {
				out = append(out, mk(a, b))
			}
		}
		return rows.Err()
	}
	if err := list(`SELECT LOWER(ROUTINE_TYPE), ROUTINE_NAME FROM information_schema.ROUTINES WHERE ROUTINE_SCHEMA = DATABASE() ORDER BY ROUTINE_NAME`,
		func(kind, name string) EditorObject { return EditorObject{Kind: kind, Name: name} }); err != nil {
		return nil, err
	}
	if err := list(`SELECT TRIGGER_NAME, EVENT_OBJECT_TABLE FROM information_schema.TRIGGERS WHERE TRIGGER_SCHEMA = DATABASE() ORDER BY EVENT_OBJECT_TABLE, TRIGGER_NAME`,
		func(name, table string) EditorObject { return EditorObject{Kind: "trigger", Name: name, Table: table} }); err != nil {
		return nil, err
	}
	// Events are optional (the scheduler tables may be unreadable): keep the rest if this fails.
	_ = list(`SELECT 'event', EVENT_NAME FROM information_schema.EVENTS WHERE EVENT_SCHEMA = DATABASE() ORDER BY EVENT_NAME`,
		func(_, name string) EditorObject { return EditorObject{Kind: "event", Name: name} })

	// Definitions, once the listing cursors are closed (one connection).
	for i := range out {
		o := &out[i]
		var stmt string
		col := 2
		switch o.Kind {
		case "procedure":
			stmt = "SHOW CREATE PROCEDURE " + c.d.quote(o.Name)
		case "function":
			stmt = "SHOW CREATE FUNCTION " + c.d.quote(o.Name)
		case "trigger":
			stmt = "SHOW CREATE TRIGGER " + c.d.quote(o.Name)
		case "event":
			stmt, col = "SHOW CREATE EVENT "+c.d.quote(o.Name), 3
		}
		o.Definition = c.showCreate(ctx, stmt, col)
	}
	return out, nil
}

// showCreate runs a SHOW CREATE statement and returns the given column, or ""
// when the user may not read it.
func (c *EditorConn) showCreate(ctx context.Context, stmt string, col int) string {
	rows, err := c.db.QueryContext(ctx, stmt)
	if err != nil {
		return ""
	}
	defer rows.Close()
	if !rows.Next() {
		return ""
	}
	cols, _ := rows.Columns()
	vals := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	if rows.Scan(ptrs...) != nil || col >= len(vals) {
		return ""
	}
	switch v := vals[col].(type) {
	case []byte:
		return string(v)
	case string:
		return v
	}
	return ""
}
