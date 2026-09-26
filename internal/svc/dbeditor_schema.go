package svc

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Schema changes for the database editor. The client never sends DDL: it sends a
// structured request and this file generates the statements, with every type
// taken from an allow-list, every existing name checked against the catalog,
// every new name validated and every literal escaped. The same request can be
// previewed (the statements are returned) or applied.

// SchemaColumn describes one column in a create/add/modify request.
type SchemaColumn struct {
	Name          string   `json:"name"`
	Type          string   `json:"type"`   // a base type from the dialect's list
	Length        string   `json:"length"` // "255", "10,2" or a fractional-seconds precision
	Values        []string `json:"values"` // enum / set members (MariaDB)
	Unsigned      bool     `json:"unsigned"`
	Nullable      bool     `json:"nullable"`
	Default       string   `json:"default"` // "", "none", "null", "value" or "current_timestamp"
	DefaultValue  string   `json:"default_value"`
	AutoIncrement bool     `json:"auto_increment"`
	Comment       string   `json:"comment"`
}

// SchemaRequest is one schema operation.
type SchemaRequest struct {
	Action     string         `json:"action"`
	Table      string         `json:"table"`
	NewName    string         `json:"new_name"`
	Name       string         `json:"name"` // existing column / index / foreign key, or the new index / key name
	Column     *SchemaColumn  `json:"column"`
	Columns    []SchemaColumn `json:"columns"` // create_table
	After      string         `json:"after"`   // MariaDB add_column position: a column name, or "" for the end
	First      bool           `json:"first"`
	KeyColumns []string       `json:"key_columns"` // primary key (create_table), index columns, foreign key columns
	Unique     bool           `json:"unique"`
	RefTable   string         `json:"ref_table"`
	RefColumns []string       `json:"ref_columns"`
	OnDelete   string         `json:"on_delete"`
	OnUpdate   string         `json:"on_update"`
	Data       bool           `json:"data"` // copy_table: include the rows
}

// SchemaResult is what a preview or an apply returns.
type SchemaResult struct {
	Statements []string `json:"statements"`
	Applied    bool     `json:"applied"`
	FailedAt   int      `json:"failed_at,omitempty"` // 1-based
	Error      string   `json:"error,omitempty"`
}

const (
	catInt = iota + 1
	catNum
	catStr
	catBin
	catTime
	catBool
	catJSON
	catEnum
)

type typeSpec struct {
	cat    int
	length string // "" (none), "len", "prec", "fsp"
	must   bool   // length is required
}

var mysqlTypes = map[string]typeSpec{
	"tinyint": {cat: catInt}, "smallint": {cat: catInt}, "mediumint": {cat: catInt}, "int": {cat: catInt}, "bigint": {cat: catInt},
	"decimal": {cat: catNum, length: "prec"}, "float": {cat: catNum}, "double": {cat: catNum},
	"bit": {cat: catBin, length: "len"}, "boolean": {cat: catBool},
	"char": {cat: catStr, length: "len"}, "varchar": {cat: catStr, length: "len", must: true},
	"tinytext": {cat: catStr}, "text": {cat: catStr}, "mediumtext": {cat: catStr}, "longtext": {cat: catStr},
	"binary": {cat: catBin, length: "len"}, "varbinary": {cat: catBin, length: "len", must: true},
	"tinyblob": {cat: catBin}, "blob": {cat: catBin}, "mediumblob": {cat: catBin}, "longblob": {cat: catBin},
	"date": {cat: catTime}, "time": {cat: catTime, length: "fsp"}, "datetime": {cat: catTime, length: "fsp"},
	"timestamp": {cat: catTime, length: "fsp"}, "year": {cat: catInt},
	"json": {cat: catJSON}, "enum": {cat: catEnum}, "set": {cat: catEnum},
}

// PostgreSQL types use the names format_type() reports, so a column's current
// type compares equal to its rendered replacement.
var pgTypes = map[string]typeSpec{
	"smallint": {cat: catInt}, "integer": {cat: catInt}, "bigint": {cat: catInt},
	"numeric": {cat: catNum, length: "prec"}, "real": {cat: catNum}, "double precision": {cat: catNum},
	"boolean":   {cat: catBool},
	"character": {cat: catStr, length: "len"}, "character varying": {cat: catStr, length: "len"}, "text": {cat: catStr},
	"bytea": {cat: catBin},
	"date":  {cat: catTime}, "time without time zone": {cat: catTime, length: "fsp"}, "timestamp without time zone": {cat: catTime, length: "fsp"},
	"timestamp with time zone": {cat: catTime, length: "fsp"}, "time with time zone": {cat: catTime, length: "fsp"},
	"uuid": {cat: catStr}, "json": {cat: catJSON}, "jsonb": {cat: catJSON},
}

// SchemaTypes lists the base types the schema editor offers for a dialect.
func SchemaTypes(kind string) []string {
	src := mysqlTypes
	order := []string{"tinyint", "smallint", "mediumint", "int", "bigint", "decimal", "float", "double", "boolean", "bit",
		"char", "varchar", "tinytext", "text", "mediumtext", "longtext", "binary", "varbinary", "tinyblob", "blob", "mediumblob", "longblob",
		"date", "time", "datetime", "timestamp", "year", "json", "enum", "set"}
	if kind == "postgres" {
		src = pgTypes
		order = []string{"smallint", "integer", "bigint", "numeric", "real", "double precision", "boolean",
			"character varying", "character", "text", "uuid", "bytea", "date", "time without time zone", "timestamp without time zone",
			"timestamp with time zone", "time with time zone", "json", "jsonb"}
	}
	out := make([]string, 0, len(order))
	for _, n := range order {
		if _, ok := src[n]; ok {
			out = append(out, n)
		}
	}
	return out
}

var (
	reLen      = regexp.MustCompile(`^[0-9]{1,5}$`)
	rePrec     = regexp.MustCompile(`^[0-9]{1,2}(,[0-9]{1,2})?$`)
	reFsp      = regexp.MustCompile(`^[0-6]$`)
	reNumLit   = regexp.MustCompile(`^-?[0-9]+(\.[0-9]+)?$`)
	reNewName  = regexp.MustCompile("^[^\\x00-\\x1f\\x7f`\"]{1,64}$")
	fkActionOK = map[string]bool{"": true, "RESTRICT": true, "CASCADE": true, "SET NULL": true, "NO ACTION": true}
)

func validNewName(kind, name string) error {
	if !reNewName.MatchString(name) || strings.TrimSpace(name) != name {
		return fmt.Errorf("%s names are 1 to 64 characters, with no quotes, control characters or leading/trailing spaces (got %q)", kind, name)
	}
	return nil
}

func (c *EditorConn) types() map[string]typeSpec {
	if c.d.kind == "postgres" {
		return pgTypes
	}
	return mysqlTypes
}

// renderType validates a column's type and returns it as SQL.
func (c *EditorConn) renderType(col *SchemaColumn) (string, typeSpec, error) {
	base := strings.ToLower(strings.TrimSpace(col.Type))
	spec, ok := c.types()[base]
	if !ok {
		return "", spec, fmt.Errorf("column %q: %q is not a supported type", col.Name, col.Type)
	}
	out := base
	length := strings.TrimSpace(col.Length)
	switch {
	case spec.cat == catEnum:
		if len(col.Values) == 0 || len(col.Values) > 200 {
			return "", spec, fmt.Errorf("column %q: list between 1 and 200 values", col.Name)
		}
		lits := make([]string, len(col.Values))
		for i, v := range col.Values {
			if len(v) > 255 {
				return "", spec, fmt.Errorf("column %q: a value is longer than 255 characters", col.Name)
			}
			lits[i] = quoteSQLString(c.d, v)
		}
		out += "(" + strings.Join(lits, ", ") + ")"
	case spec.length == "":
		if length != "" {
			return "", spec, fmt.Errorf("column %q: %s takes no length", col.Name, base)
		}
	case length == "":
		if spec.must {
			return "", spec, fmt.Errorf("column %q: %s needs a length", col.Name, base)
		}
	default:
		re := map[string]*regexp.Regexp{"len": reLen, "prec": rePrec, "fsp": reFsp}[spec.length]
		if !re.MatchString(length) {
			return "", spec, fmt.Errorf("column %q: %q is not a valid length for %s", col.Name, length, base)
		}
		if spec.length == "len" {
			if n, _ := strconv.Atoi(length); n < 1 || n > 65535 {
				return "", spec, fmt.Errorf("column %q: length must be between 1 and 65535", col.Name)
			}
		}
		out += "(" + length + ")"
	}
	if col.Unsigned && c.d.kind == "mariadb" && (spec.cat == catInt || spec.cat == catNum) && base != "year" {
		out += " unsigned"
	}
	return out, spec, nil
}

// boolLiteral renders a boolean default for the dialect.
func (c *EditorConn) boolLiteral(on bool) string {
	switch {
	case c.d.kind == "postgres" && on:
		return "TRUE"
	case c.d.kind == "postgres":
		return "FALSE"
	case on:
		return "1"
	}
	return "0"
}

// renderDefault returns "" (no clause) or " DEFAULT ...".
func (c *EditorConn) renderDefault(col *SchemaColumn, base string, spec typeSpec) (string, error) {
	switch col.Default {
	case "", "none":
		return "", nil
	case "null":
		if !col.Nullable {
			return "", fmt.Errorf("column %q: a NOT NULL column cannot default to NULL", col.Name)
		}
		return " DEFAULT NULL", nil
	case "current_timestamp":
		isStamp := strings.Contains(base, "stamp") || base == "datetime"
		if spec.cat != catTime || !isStamp {
			return "", fmt.Errorf("column %q: CURRENT_TIMESTAMP only suits timestamp and datetime columns", col.Name)
		}
		return " DEFAULT CURRENT_TIMESTAMP", nil
	case "value":
		v := col.DefaultValue
		switch spec.cat {
		case catInt, catNum:
			if !reNumLit.MatchString(v) {
				return "", fmt.Errorf("column %q: %q is not a number", col.Name, v)
			}
			return " DEFAULT " + v, nil
		case catBool:
			switch strings.ToLower(v) {
			case "1", "true":
				return " DEFAULT " + c.boolLiteral(true), nil
			case "0", "false":
				return " DEFAULT " + c.boolLiteral(false), nil
			}
			return "", fmt.Errorf("column %q: a boolean default is true or false", col.Name)
		}
		if len(v) > 4096 {
			return "", fmt.Errorf("column %q: the default is too long", col.Name)
		}
		return " DEFAULT " + quoteSQLString(c.d, v), nil
	}
	return "", fmt.Errorf("column %q: unknown default mode %q", col.Name, col.Default)
}

func pgSerialFor(base string) (string, bool) {
	switch base {
	case "smallint":
		return "smallserial", true
	case "integer":
		return "serial", true
	case "bigint":
		return "bigserial", true
	}
	return "", false
}

// columnDDL renders "name type [attrs]" for CREATE TABLE and ADD/CHANGE COLUMN.
func (c *EditorConn) columnDDL(col *SchemaColumn) (string, error) {
	if err := validNewName("column", col.Name); err != nil {
		return "", err
	}
	typ, spec, err := c.renderType(col)
	if err != nil {
		return "", err
	}
	base := strings.ToLower(strings.TrimSpace(col.Type))
	def, err := c.renderDefault(col, base, spec)
	if err != nil {
		return "", err
	}
	line := c.d.quote(col.Name) + " "
	if c.d.kind == "postgres" {
		if col.AutoIncrement {
			serial, ok := pgSerialFor(base)
			if !ok {
				return "", fmt.Errorf("column %q: only smallint, integer and bigint can auto-increment", col.Name)
			}
			return line + serial, nil // implies NOT NULL and its own default
		}
		line += typ
		if !col.Nullable {
			line += " NOT NULL"
		}
		return line + def, nil
	}
	line += typ
	if col.Nullable {
		line += " NULL"
	} else {
		line += " NOT NULL"
	}
	if col.AutoIncrement {
		if spec.cat != catInt || base == "year" {
			return "", fmt.Errorf("column %q: only integer columns can auto-increment", col.Name)
		}
		line += " AUTO_INCREMENT"
	} else {
		line += def
	}
	if col.Comment != "" {
		if len(col.Comment) > 1024 {
			return "", fmt.Errorf("column %q: the comment is too long", col.Name)
		}
		line += " COMMENT " + quoteSQLString(c.d, col.Comment)
	}
	return line, nil
}

// existing names must be real: they are then safe to quote into a statement.
func mustHave(names map[string]bool, kind, name string) error {
	if !names[name] {
		return fmt.Errorf("%s %q does not exist", kind, name)
	}
	return nil
}

func indexNames(st *EditorStructure) map[string]bool {
	m := map[string]bool{}
	for _, ix := range st.Indexes {
		m[ix.Name] = true
	}
	return m
}

func fkNames(st *EditorStructure) map[string]bool {
	m := map[string]bool{}
	for _, fk := range st.ForeignKeys {
		m[fk.Name] = true
	}
	return m
}

func requireColumns(have map[string]bool, cols []string) error {
	if len(cols) == 0 || len(cols) > 32 {
		return errors.New("choose between 1 and 32 columns")
	}
	seen := map[string]bool{}
	for _, n := range cols {
		if err := mustHave(have, "column", n); err != nil {
			return err
		}
		if seen[n] {
			return fmt.Errorf("column %q is listed twice", n)
		}
		seen[n] = true
	}
	return nil
}

func (c *EditorConn) requireNewTable(ctx context.Context, name string) error {
	if err := validNewName("table", name); err != nil {
		return err
	}
	if _, err := c.requireTable(ctx, name); err == nil {
		return fmt.Errorf("%q already exists", name)
	}
	return nil
}

// planCreateTable builds a CREATE TABLE statement.
func (c *EditorConn) planCreateTable(ctx context.Context, req SchemaRequest) ([]string, error) {
	if err := c.requireNewTable(ctx, req.Table); err != nil {
		return nil, err
	}
	if len(req.Columns) == 0 || len(req.Columns) > 500 {
		return nil, errors.New("a table needs between 1 and 500 columns")
	}
	names := map[string]bool{}
	var lines []string
	autoCol := ""
	for i := range req.Columns {
		col := &req.Columns[i]
		if names[col.Name] {
			return nil, fmt.Errorf("column %q is listed twice", col.Name)
		}
		names[col.Name] = true
		line, err := c.columnDDL(col)
		if err != nil {
			return nil, err
		}
		if col.AutoIncrement {
			if autoCol != "" {
				return nil, errors.New("only one column can auto-increment")
			}
			autoCol = col.Name
		}
		lines = append(lines, line)
	}
	pk := req.KeyColumns
	if len(pk) > 0 {
		if err := requireColumns(names, pk); err != nil {
			return nil, fmt.Errorf("primary key: %w", err)
		}
		lines = append(lines, "PRIMARY KEY ("+quoteList(c.d, pk)+")")
	}
	if autoCol != "" && c.d.kind != "postgres" {
		inPK := false
		for _, k := range pk {
			inPK = inPK || k == autoCol
		}
		if !inPK {
			return nil, fmt.Errorf("the auto-increment column %q must be part of the primary key", autoCol)
		}
	}
	return []string{"CREATE TABLE " + c.d.quote(req.Table) + " (\n  " + strings.Join(lines, ",\n  ") + "\n)"}, nil
}

// PlanSchema turns a request into the statements that carry it out. It reads the
// catalog but changes nothing.
func (c *EditorConn) PlanSchema(ctx context.Context, req SchemaRequest) ([]string, error) {
	q := c.d.quote
	pg := c.d.kind == "postgres"

	if req.Action == "create_table" {
		return c.planCreateTable(ctx, req)
	}

	st, err := c.Structure(ctx, req.Table)
	if err != nil {
		return nil, err
	}
	have := columnSet(st)
	tbl := q(st.Table)
	if st.Type == "view" && req.Action != "rename_table" {
		return nil, errors.New("this operation is for tables, not views")
	}

	switch req.Action {
	case "rename_table":
		if err := c.requireNewTable(ctx, req.NewName); err != nil {
			return nil, err
		}
		if pg {
			kw := "TABLE"
			if st.Type == "view" {
				kw = "VIEW"
			}
			return []string{"ALTER " + kw + " " + tbl + " RENAME TO " + q(req.NewName)}, nil
		}
		return []string{"RENAME TABLE " + tbl + " TO " + q(req.NewName)}, nil

	case "copy_table":
		if err := c.requireNewTable(ctx, req.NewName); err != nil {
			return nil, err
		}
		nt := q(req.NewName)
		var out []string
		if pg {
			out = append(out, "CREATE TABLE "+nt+" (LIKE "+tbl+" INCLUDING ALL)")
		} else {
			out = append(out, "CREATE TABLE "+nt+" LIKE "+tbl)
		}
		if req.Data {
			out = append(out, "INSERT INTO "+nt+" SELECT * FROM "+tbl)
		}
		return out, nil

	case "add_column":
		if req.Column == nil {
			return nil, errors.New("describe the new column")
		}
		if have[req.Column.Name] {
			return nil, fmt.Errorf("column %q already exists", req.Column.Name)
		}
		line, err := c.columnDDL(req.Column)
		if err != nil {
			return nil, err
		}
		stmt := "ALTER TABLE " + tbl + " ADD COLUMN " + line
		if !pg {
			switch {
			case req.First:
				stmt += " FIRST"
			case req.After != "":
				if err := mustHave(have, "column", req.After); err != nil {
					return nil, err
				}
				stmt += " AFTER " + q(req.After)
			}
		}
		out := []string{stmt}
		if pg && req.Column.Comment != "" {
			out = append(out, "COMMENT ON COLUMN "+tbl+"."+q(req.Column.Name)+" IS "+quoteSQLString(c.d, req.Column.Comment))
		}
		return out, nil

	case "drop_column":
		if err := mustHave(have, "column", req.Name); err != nil {
			return nil, err
		}
		if len(st.Columns) == 1 {
			return nil, errors.New("a table must keep at least one column; drop the table instead")
		}
		return []string{"ALTER TABLE " + tbl + " DROP COLUMN " + q(req.Name)}, nil

	case "modify_column":
		if req.Column == nil {
			return nil, errors.New("describe the column")
		}
		if err := mustHave(have, "column", req.Name); err != nil {
			return nil, err
		}
		if req.Column.Name != req.Name && have[req.Column.Name] {
			return nil, fmt.Errorf("column %q already exists", req.Column.Name)
		}
		line, err := c.columnDDL(req.Column)
		if err != nil {
			return nil, err
		}
		if !pg {
			return []string{"ALTER TABLE " + tbl + " CHANGE COLUMN " + q(req.Name) + " " + line}, nil
		}
		return c.planPGModify(st, req)

	case "add_index":
		if err := validNewName("index", req.Name); err != nil {
			return nil, err
		}
		if indexNames(st)[req.Name] {
			return nil, fmt.Errorf("index %q already exists", req.Name)
		}
		if err := requireColumns(have, req.KeyColumns); err != nil {
			return nil, err
		}
		unique := ""
		if req.Unique {
			unique = "UNIQUE "
		}
		return []string{"CREATE " + unique + "INDEX " + q(req.Name) + " ON " + tbl + " (" + quoteList(c.d, req.KeyColumns) + ")"}, nil

	case "drop_index":
		if err := mustHave(indexNames(st), "index", req.Name); err != nil {
			return nil, err
		}
		for _, ix := range st.Indexes {
			if ix.Name == req.Name && ix.Primary {
				return nil, errors.New("the primary key cannot be dropped here")
			}
		}
		if pg {
			return []string{"DROP INDEX " + q(req.Name)}, nil
		}
		return []string{"ALTER TABLE " + tbl + " DROP INDEX " + q(req.Name)}, nil

	case "add_foreign_key":
		return c.planAddFK(ctx, st, req)

	case "drop_foreign_key":
		if err := mustHave(fkNames(st), "foreign key", req.Name); err != nil {
			return nil, err
		}
		if pg {
			return []string{"ALTER TABLE " + tbl + " DROP CONSTRAINT " + q(req.Name)}, nil
		}
		return []string{"ALTER TABLE " + tbl + " DROP FOREIGN KEY " + q(req.Name)}, nil
	}
	return nil, fmt.Errorf("unknown action %q", req.Action)
}

func (c *EditorConn) planAddFK(ctx context.Context, st *EditorStructure, req SchemaRequest) ([]string, error) {
	q := c.d.quote
	if err := validNewName("foreign key", req.Name); err != nil {
		return nil, err
	}
	if fkNames(st)[req.Name] {
		return nil, fmt.Errorf("foreign key %q already exists", req.Name)
	}
	if err := requireColumns(columnSet(st), req.KeyColumns); err != nil {
		return nil, err
	}
	ref, err := c.Structure(ctx, req.RefTable)
	if err != nil {
		return nil, err
	}
	if ref.Type == "view" {
		return nil, errors.New("a foreign key cannot reference a view")
	}
	if err := requireColumns(columnSet(ref), req.RefColumns); err != nil {
		return nil, fmt.Errorf("referenced columns: %w", err)
	}
	if len(req.RefColumns) != len(req.KeyColumns) {
		return nil, errors.New("the foreign key and the referenced key need the same number of columns")
	}
	stmt := "ALTER TABLE " + q(st.Table) + " ADD CONSTRAINT " + q(req.Name) + " FOREIGN KEY (" + quoteList(c.d, req.KeyColumns) +
		") REFERENCES " + q(ref.Table) + " (" + quoteList(c.d, req.RefColumns) + ")"
	for _, a := range []struct{ clause, val string }{{"ON DELETE", req.OnDelete}, {"ON UPDATE", req.OnUpdate}} {
		v := strings.ToUpper(strings.TrimSpace(a.val))
		if !fkActionOK[v] {
			return nil, fmt.Errorf("%s must be RESTRICT, CASCADE, SET NULL or NO ACTION", a.clause)
		}
		if v != "" {
			stmt += " " + a.clause + " " + v
		}
	}
	return []string{stmt}, nil
}

// planPGModify builds the several ALTERs PostgreSQL needs to change one column.
func (c *EditorConn) planPGModify(st *EditorStructure, req SchemaRequest) ([]string, error) {
	q := c.d.quote
	tbl := q(st.Table)
	var cur *EditorColumn
	for i := range st.Columns {
		if st.Columns[i].Name == req.Name {
			cur = &st.Columns[i]
		}
	}
	col := req.Column
	base := strings.ToLower(strings.TrimSpace(col.Type))
	typ, spec, err := c.renderType(col)
	if err != nil {
		return nil, err
	}
	if col.AutoIncrement != (cur.Extra == "auto_increment") {
		return nil, errors.New("auto-increment cannot be switched on or off for an existing PostgreSQL column; add a new column instead")
	}
	var out []string
	name := req.Name
	if col.Name != req.Name {
		out = append(out, "ALTER TABLE "+tbl+" RENAME COLUMN "+q(req.Name)+" TO "+q(col.Name))
		name = col.Name
	}
	qc := q(name)
	if !col.AutoIncrement {
		if !strings.EqualFold(typ, cur.Type) {
			out = append(out, "ALTER TABLE "+tbl+" ALTER COLUMN "+qc+" TYPE "+typ+" USING "+qc+"::"+typ)
		}
		if col.Nullable != cur.Nullable {
			verb := "SET NOT NULL"
			if col.Nullable {
				verb = "DROP NOT NULL"
			}
			out = append(out, "ALTER TABLE "+tbl+" ALTER COLUMN "+qc+" "+verb)
		}
		def, err := c.renderDefault(col, base, spec)
		if err != nil {
			return nil, err
		}
		curDef := ""
		if cur.Default != nil {
			curDef = " DEFAULT " + *cur.Default
		}
		if strings.TrimSpace(def) != strings.TrimSpace(curDef) {
			if def == "" {
				out = append(out, "ALTER TABLE "+tbl+" ALTER COLUMN "+qc+" DROP DEFAULT")
			} else {
				out = append(out, "ALTER TABLE "+tbl+" ALTER COLUMN "+qc+" SET"+def)
			}
		}
	}
	if col.Comment != cur.Comment {
		lit := "NULL"
		if col.Comment != "" {
			lit = quoteSQLString(c.d, col.Comment)
		}
		out = append(out, "COMMENT ON COLUMN "+tbl+"."+qc+" IS "+lit)
	}
	if len(out) == 0 {
		return nil, errors.New("nothing to change")
	}
	return out, nil
}

// ApplySchema previews (preview=true) or runs a schema request. PostgreSQL runs
// the statements in one transaction; MariaDB commits DDL implicitly, so a
// failure part-way leaves the earlier statements applied and reports which one
// failed. audit is called with each statement just before it runs.
func (c *EditorConn) ApplySchema(ctx context.Context, req SchemaRequest, preview bool, audit func(stmt string)) (*SchemaResult, error) {
	stmts, err := c.PlanSchema(ctx, req)
	if err != nil {
		return nil, err
	}
	res := &SchemaResult{Statements: stmts}
	if preview {
		return res, nil
	}
	type execer interface {
		ExecContext(context.Context, string, ...any) (sql.Result, error)
	}
	var ex execer = c.db
	var tx *sql.Tx
	if c.d.kind == "postgres" {
		if tx, err = c.db.BeginTx(ctx, nil); err != nil {
			return nil, err
		}
		defer tx.Rollback()
		ex = tx
	}
	for i, s := range stmts {
		if audit != nil {
			audit(s)
		}
		if _, err := ex.ExecContext(ctx, s); err != nil {
			res.FailedAt, res.Error = i+1, err.Error()
			return res, err
		}
	}
	if tx != nil {
		if err := tx.Commit(); err != nil {
			res.Error = err.Error()
			return res, err
		}
	}
	res.Applied = true
	return res, nil
}

// Maintenance runs a table maintenance command and returns its result (MariaDB
// reports a status table; PostgreSQL returns nothing).
func (c *EditorConn) Maintenance(ctx context.Context, table, op string) (*EditorResult, string, error) {
	typ, err := c.requireTable(ctx, table)
	if err != nil {
		return nil, "", err
	}
	if typ == "view" {
		return nil, "", errors.New("maintenance applies to tables, not views")
	}
	tbl := c.d.quote(table)
	pg := c.d.kind == "postgres"
	var stmt string
	switch {
	case pg && op == "analyze":
		stmt = "ANALYZE " + tbl
	case pg && op == "vacuum":
		stmt = "VACUUM " + tbl
	case pg && op == "reindex":
		stmt = "REINDEX TABLE " + tbl
	case !pg && op == "analyze":
		stmt = "ANALYZE TABLE " + tbl
	case !pg && op == "optimize":
		stmt = "OPTIMIZE TABLE " + tbl
	case !pg && op == "check":
		stmt = "CHECK TABLE " + tbl
	case !pg && op == "repair":
		stmt = "REPAIR TABLE " + tbl
	default:
		return nil, "", fmt.Errorf("%q is not a maintenance operation for this database", op)
	}
	if !pg {
		rows, err := c.db.QueryContext(ctx, stmt)
		if err != nil {
			return nil, stmt, err
		}
		defer rows.Close()
		res, err := scanRows(rows, 100, editorMaxBytes)
		return res, stmt, err
	}
	_, err = c.db.ExecContext(ctx, stmt)
	return nil, stmt, err
}
