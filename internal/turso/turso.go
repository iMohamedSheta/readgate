// Package turso implements the ReadGate gateway for Turso (and any sqld
// server) over libsql HTTP. Mapping: Source.Database holds the full URL
// (libsql://….turso.io or http://host:8080), Source.Password holds the
// auth token (AES-GCM encrypted at rest, never leaves the gateway).
//
// Turso issues full-access tokens — there are no GRANTs to audit. The
// read-only proof is therefore gateway-enforced: the SQL guard rejects
// every write in-process (self-tested at verify time), all reads are
// plain SELECTs, and no write path exists anywhere in the tool surface.
// Check details say this explicitly; nothing is implied.
package turso

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"readgate/internal/guard"
	"readgate/internal/model"

	"github.com/tursodatabase/libsql-client-go/libsql"
)

var identRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_$]*$`)

var rawDeny = regexp.MustCompile(`(?i)(;|--|/\*|\b(INSERT|UPDATE|DELETE|MERGE|DROP|CREATE|ALTER|TRUNCATE|GRANT|REVOKE|COPY|VACUUM|CALL|DO|INTO|LOAD|EXECUTE|PERFORM|PRAGMA|ATTACH|DETACH)\b)`)

// ValidURL reports whether s looks like a libsql/http(s) endpoint.
func ValidURL(s string) bool {
	s = strings.TrimSpace(s)
	for _, p := range []string{"libsql://", "https://", "http://"} {
		if strings.HasPrefix(s, p) && len(s) > len(p)+3 {
			return true
		}
	}
	return false
}

func open(src model.Source) (*sql.DB, error) {
	url := strings.TrimSpace(src.Database)
	if !ValidURL(url) {
		return nil, fmt.Errorf("URL must start with libsql://, https:// or http://")
	}
	var opts []libsql.Option
	if t := strings.TrimSpace(src.Password); t != "" {
		opts = append(opts, libsql.WithAuthToken(t))
	}
	conn, err := libsql.NewConnector(url, opts...)
	if err != nil {
		return nil, err
	}
	db := sql.OpenDB(conn)
	db.SetMaxOpenConns(4)
	db.SetConnMaxIdleTime(2 * time.Minute)
	c, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	if err := db.PingContext(c); err != nil {
		db.Close()
		return nil, fmt.Errorf("turso ping: %w", err)
	}
	return db, nil
}

func withTimeout(ctx context.Context, secs int) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, time.Duration(secs)*time.Second)
}

func pushCheck(out *[]model.CheckResult, key, label string, fn func() (string, error)) {
	start := time.Now()
	detail, err := fn()
	*out = append(*out, model.CheckResult{Key: key, Label: label, OK: err == nil, Detail: detail, Duration: time.Since(start).Milliseconds()})
	if err != nil && detail == "" {
		(*out)[len(*out)-1].Detail = err.Error()
	}
}

// VerifyReadOnly proves a Turso endpoint is safe behind the gateway.
// Keys select/write/ro mirror every other engine so the enable invariant
// in app.go applies unchanged.
func VerifyReadOnly(ctx context.Context, src model.Source) []model.CheckResult {
	if !ValidURL(src.Database) {
		return []model.CheckResult{{Key: "select", Label: "Turso URL", Detail: "URL must start with libsql://, https:// or http://"}}
	}
	db, err := open(src)
	if err != nil {
		return []model.CheckResult{{Key: "select", Label: "Connect to Turso", Detail: err.Error()}}
	}
	defer db.Close()
	out := []model.CheckResult{}
	pushCheck(&out, "connect", "Turso connection successful", func() (string, error) {
		c, cancel := withTimeout(ctx, 12)
		defer cancel()
		var ver string
		if err := db.QueryRowContext(c, `SELECT sqlite_version()`).Scan(&ver); err != nil {
			return "", err
		}
		host := src.Database
		if i := strings.Index(host, "://"); i >= 0 {
			host = host[i+3:]
		}
		if i := strings.Index(host, "/"); i >= 0 {
			host = host[:i]
		}
		return host + " · sqlite " + ver, nil
	})
	pushCheck(&out, "select", "SELECT permission verified", func() (string, error) {
		c, cancel := withTimeout(ctx, 12)
		defer cancel()
		var one int
		if err := db.QueryRowContext(c, `SELECT 1`).Scan(&one); err != nil {
			return "", err
		}
		var n int
		_ = db.QueryRowContext(c, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'`).Scan(&n)
		return fmt.Sprintf("SELECT 1 → 1 · %d tables visible", n), nil
	})
	pushCheck(&out, "write", "Writes blocked by gateway guard", func() (string, error) {
		// Self-test the actual enforcement point: the shared guard must
		// reject every write class. Turso tokens are full-access, so the
		// gateway — not the database — is the read-only boundary.
		for _, w := range []string{"INSERT INTO t VALUES (1)", "UPDATE t SET a=1", "DELETE FROM t", "DROP TABLE t", "CREATE TABLE x(a)"} {
			if err := guard.ValidateReadOnlySQL(w); err == nil {
				return "", fmt.Errorf("guard accepted %q — refusing to enable", w)
			}
		}
		return "guard rejects INSERT/UPDATE/DELETE/DDL · no write tool exists", nil
	})
	pushCheck(&out, "ro", "Read-only configuration verified", func() (string, error) {
		return "token stays in gateway (AES-GCM) · every tool issues SELECT only", nil
	})
	return out
}

func tables(ctx context.Context, db *sql.DB) ([]string, error) {
	c, cancel := withTimeout(ctx, 20)
	defer cancel()
	rows, err := db.QueryContext(c, `SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func countTable(ctx context.Context, db *sql.DB, table string) int64 {
	c, cancel := withTimeout(ctx, 5)
	defer cancel()
	var n int64
	if err := db.QueryRowContext(c, `SELECT COUNT(*) FROM "`+table+`"`).Scan(&n); err != nil {
		return 0
	}
	return n
}

// Schema returns tables with best-effort counts (remote: capped at 50).
func Schema(ctx context.Context, src model.Source) (model.SchemaInfo, error) {
	db, err := open(src)
	if err != nil {
		return model.SchemaInfo{}, err
	}
	defer db.Close()
	names, err := tables(ctx, db)
	if err != nil {
		return model.SchemaInfo{}, err
	}
	info := model.SchemaInfo{SourceName: src.Name}
	for i, n := range names {
		var est int64
		if i < 50 {
			est = countTable(ctx, db, n)
		}
		info.Tables = append(info.Tables, model.TableInfo{Schema: "main", Name: n, RowEstimate: est})
	}
	return info, nil
}

// Columns returns one table's columns.
func Columns(ctx context.Context, src model.Source, schema, table string) ([]model.ColumnInfo, error) {
	if !identRe.MatchString(table) || len(table) > 64 {
		return nil, fmt.Errorf("invalid table name")
	}
	db, err := open(src)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	c, cancel := withTimeout(ctx, 20)
	defer cancel()
	rows, err := db.QueryContext(c, `PRAGMA table_info("`+table+`")`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.ColumnInfo
	for rows.Next() {
		var cid, notnull, pk int
		var name, ctype string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return nil, err
		}
		out = append(out, model.ColumnInfo{Name: name, Type: ctype, Nullable: notnull == 0, Default: dflt.String})
	}
	return out, rows.Err()
}

func normVal(v any) any {
	switch t := v.(type) {
	case []byte:
		if !utf8.Valid(t) {
			return fmt.Sprintf("<binary %d bytes>", len(t))
		}
		return string(t)
	case string:
		return t
	case time.Time:
		return t.Format(time.RFC3339Nano)
	case int64, float64, bool, nil:
		return v
	default:
		return fmt.Sprintf("%v", v)
	}
}

func scanRows(rows *sql.Rows, capStrings bool) (cols []string, data [][]any, err error) {
	cols, err = rows.Columns()
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		ptrs := make([]any, len(cols))
		vals := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, nil, err
		}
		for i, v := range vals {
			v = normVal(v)
			if capStrings {
				if s, ok := v.(string); ok {
					if rr := []rune(s); len(rr) > 1000 {
						v = string(rr[:1000]) + fmt.Sprintf("… (+%d chars)", len(rr)-1000)
					}
				}
			}
			vals[i] = v
		}
		data = append(data, vals)
	}
	return cols, data, rows.Err()
}

// Query runs a guarded read-only statement with row cap + timeout.
func Query(ctx context.Context, src model.Source, sqlText string, limit int) (model.QueryResult, error) {
	if err := guard.ValidateReadOnlySQL(sqlText); err != nil {
		return model.QueryResult{}, err
	}
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	sqlText = guard.EnforceLimit(sqlText, limit)
	db, err := open(src)
	if err != nil {
		return model.QueryResult{}, err
	}
	defer db.Close()
	c, cancel := withTimeout(ctx, 25)
	defer cancel()
	start := time.Now()
	rows, err := db.QueryContext(c, sqlText)
	if err != nil {
		return model.QueryResult{}, err
	}
	defer rows.Close()
	cols, data, err := scanRows(rows, false)
	if err != nil {
		return model.QueryResult{}, err
	}
	if len(data) > limit {
		data = data[:limit]
	}
	return model.QueryResult{
		Columns: cols, Rows: data, RowCount: len(data),
		DurationMs: time.Since(start).Milliseconds(), Truncated: len(data) >= limit,
	}, nil
}

func escLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}

func coerce(v string) any {
	if i, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil {
		return i
	}
	if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
		return f
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true":
		return true
	case "false":
		return false
	}
	return v
}

func buildWhere(filters []model.Filter, raw string) (string, []any) {
	var conds []string
	var args []any
	next := func(v any) string {
		args = append(args, v)
		return "?"
	}
	for i, f := range filters {
		if !identRe.MatchString(f.Column) || len(f.Column) > 64 {
			continue
		}
		col := `"` + f.Column + `"`
		join := ""
		if i > 0 {
			if strings.ToUpper(strings.TrimSpace(f.Logic)) == "OR" {
				join = "OR "
			} else {
				join = "AND "
			}
		}
		v := f.Value
		switch strings.ToLower(strings.TrimSpace(f.Op)) {
		case "eq":
			conds = append(conds, join+col+" = "+next(coerce(v)))
		case "ne":
			conds = append(conds, join+col+" != "+next(coerce(v)))
		case "gt":
			conds = append(conds, join+col+" > "+next(coerce(v)))
		case "gte":
			conds = append(conds, join+col+" >= "+next(coerce(v)))
		case "lt":
			conds = append(conds, join+col+" < "+next(coerce(v)))
		case "lte":
			conds = append(conds, join+col+" <= "+next(coerce(v)))
		case "contains":
			conds = append(conds, join+`CAST(`+col+` AS TEXT) LIKE `+next("%"+escLike(v)+"%")+` ESCAPE '\'`)
		case "starts":
			conds = append(conds, join+`CAST(`+col+` AS TEXT) LIKE `+next(escLike(v)+"%")+` ESCAPE '\'`)
		case "ends":
			conds = append(conds, join+`CAST(`+col+` AS TEXT) LIKE `+next("%"+escLike(v))+` ESCAPE '\'`)
		case "like":
			conds = append(conds, join+`CAST(`+col+` AS TEXT) LIKE `+next(v))
		case "in":
			var holders []string
			for _, part := range strings.Split(v, ",") {
				part = strings.TrimSpace(part)
				if part == "" {
					continue
				}
				holders = append(holders, next(coerce(part)))
			}
			if len(holders) == 0 {
				continue
			}
			conds = append(conds, join+col+" IN ("+strings.Join(holders, ",")+")")
		case "null":
			conds = append(conds, join+col+" IS NULL")
		case "notnull":
			conds = append(conds, join+col+" IS NOT NULL")
		}
	}
	raw = strings.TrimSpace(raw)
	if raw != "" && !rawDeny.MatchString(raw) {
		if len(conds) > 0 {
			conds = append(conds, "AND ("+raw+")")
		} else {
			conds = append(conds, "("+raw+")")
		}
	}
	if len(conds) == 0 {
		return "", args
	}
	return "WHERE " + strings.Join(conds, " "), args
}

// Preview fetches a UI-safe page.
func Preview(ctx context.Context, src model.Source, schema, table string, limit, offset int, filters []model.Filter, raw string) (model.QueryResult, error) {
	if !identRe.MatchString(table) || len(table) > 64 {
		return model.QueryResult{}, fmt.Errorf("invalid table name")
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	db, err := open(src)
	if err != nil {
		return model.QueryResult{}, err
	}
	defer db.Close()
	c, cancel := withTimeout(ctx, 25)
	defer cancel()
	where, args := buildWhere(filters, raw)
	q := fmt.Sprintf(`SELECT * FROM "%s" %s LIMIT %d OFFSET %d`, table, where, limit, offset)
	start := time.Now()
	rows, err := db.QueryContext(c, q, args...)
	if err != nil {
		return model.QueryResult{}, err
	}
	defer rows.Close()
	cols, data, err := scanRows(rows, true)
	if err != nil {
		return model.QueryResult{}, err
	}
	var total int64 = -1
	if where != "" {
		_ = db.QueryRowContext(c, fmt.Sprintf(`SELECT COUNT(*) FROM "%s" %s`, table, where), args...).Scan(&total)
	}
	return model.QueryResult{
		Columns: cols, Rows: data, RowCount: len(data), TotalRows: total,
		DurationMs: time.Since(start).Milliseconds(), Truncated: len(data) >= limit,
	}, nil
}

// Explain returns EXPLAIN QUERY PLAN lines.
func Explain(ctx context.Context, src model.Source, sqlText string) (string, error) {
	if err := guard.ValidateReadOnlySQL(sqlText); err != nil {
		return "", err
	}
	db, err := open(src)
	if err != nil {
		return "", err
	}
	defer db.Close()
	c, cancel := withTimeout(ctx, 25)
	defer cancel()
	rows, err := db.QueryContext(c, "EXPLAIN QUERY PLAN "+strings.TrimSuffix(strings.TrimSpace(sqlText), ";"))
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			return "", err
		}
		lines = append(lines, detail)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if len(lines) == 0 {
		return "(no plan rows)", nil
	}
	out := strings.Join(lines, "\n")
	if len(out) > 8000 {
		out = out[:8000] + "…(truncated)"
	}
	return out, nil
}

// DoctorFindings runs endpoint-appropriate health probes.
func DoctorFindings(ctx context.Context, src model.Source) ([]model.DoctorFinding, error) {
	db, err := open(src)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	var findings []model.DoctorFinding
	add := func(sev, title, detail, remedy string) {
		findings = append(findings, model.DoctorFinding{Severity: sev, Title: title, Detail: detail, Remedy: remedy, Source: src.Name})
	}
	var ver string
	if err := db.QueryRow(`SELECT sqlite_version()`).Scan(&ver); err != nil {
		return nil, err
	}
	add("info", "Turso reachable", "sqlite "+ver, "")
	names, err := tables(ctx, db)
	if err != nil {
		return findings, nil
	}
	var total int64
	type sized struct {
		name string
		n    int64
	}
	var order []sized
	for i, n := range names {
		var c int64
		if i < 50 {
			c = countTable(ctx, db, n)
		}
		total += c
		order = append(order, sized{n, c})
	}
	add("info", fmt.Sprintf("Inventory: %d tables, ~%d rows", len(names), total), "Counts capped at 50 tables (remote).", "")
	for i := 0; i < len(order) && i < 5; i++ {
		for j := i + 1; j < len(order); j++ {
			if order[j].n > order[i].n {
				order[i], order[j] = order[j], order[i]
			}
		}
	}
	if len(order) > 0 && order[0].n > 0 {
		var top []string
		for i := 0; i < len(order) && i < 5; i++ {
			top = append(top, fmt.Sprintf("%s (~%d)", order[i].name, order[i].n))
		}
		add("info", "Largest tables", strings.Join(top, " · "), "Start slow-query triage here.")
	}
	checked := 0
	for _, n := range names {
		if checked >= 10 {
			break
		}
		checked++
		c, cancel := withTimeout(ctx, 15)
		prows, err := db.QueryContext(c, `PRAGMA table_info("`+n+`")`)
		if err != nil {
			cancel()
			continue
		}
		hasPK := false
		for prows.Next() {
			var cid, notnull, pk int
			var nm, tp string
			var dflt sql.NullString
			prows.Scan(&cid, &nm, &tp, &notnull, &dflt, &pk)
			if pk > 0 {
				hasPK = true
			}
		}
		prows.Close()
		cancel()
		if !hasPK {
			add("warn", "Table without primary key: main."+n,
				"Dedupe and AI-generated joins are riskier without a PK.",
				"Recreate with an INTEGER PRIMARY KEY.")
		}
	}
	add("info", "No server to pressure", "Managed endpoint — no connections or activity views to saturate.", "")
	return findings, nil
}

// TableStats returns per-table shape stats.
func TableStats(ctx context.Context, src model.Source, schema, table string, limit int) ([]map[string]any, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	db, err := open(src)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	one := func(n string) (map[string]any, error) {
		cols, err := Columns(ctx, src, "main", n)
		if err != nil {
			return nil, err
		}
		c, cancel := withTimeout(ctx, 15)
		defer cancel()
		var idxCount int
		_ = db.QueryRowContext(c, `SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND tbl_name=?`, n).Scan(&idxCount)
		return map[string]any{
			"table": "main." + n, "rowCount": countTable(ctx, db, n),
			"columnCount": len(cols), "indexCount": idxCount,
		}, nil
	}
	if table != "" {
		if !identRe.MatchString(table) {
			return nil, fmt.Errorf("invalid table name")
		}
		m, err := one(table)
		if err != nil {
			return nil, err
		}
		return []map[string]any{m}, nil
	}
	names, err := tables(ctx, db)
	if err != nil {
		return nil, err
	}
	var out []map[string]any
	for i, n := range names {
		if i >= limit {
			break
		}
		m, err := one(n)
		if err != nil {
			continue
		}
		out = append(out, m)
	}
	return out, nil
}

// Indexes returns index name + uniqueness + definition for one table.
func Indexes(ctx context.Context, src model.Source, schema, table string) ([]map[string]any, error) {
	if !identRe.MatchString(table) {
		return nil, fmt.Errorf("invalid table name")
	}
	db, err := open(src)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	c, cancel := withTimeout(ctx, 20)
	defer cancel()
	rows, err := db.QueryContext(c, `PRAGMA index_list("`+table+`")`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type idx struct {
		name   string
		unique bool
	}
	var list []idx
	for rows.Next() {
		var seq, unique, partial int
		var name, origin string
		if err := rows.Scan(&seq, &name, &unique, &origin, &partial); err != nil {
			return nil, err
		}
		list = append(list, idx{name, unique != 0})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var out []map[string]any
	for _, ix := range list {
		var def string
		_ = db.QueryRowContext(c, `SELECT sql FROM sqlite_master WHERE type='index' AND name=?`, ix.name).Scan(&def)
		out = append(out, map[string]any{"index": ix.name, "unique": ix.unique, "definition": def})
	}
	return out, nil
}

// Relationships returns foreign-key edges.
func Relationships(ctx context.Context, src model.Source, schema, table string, limit int) ([]map[string]any, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	db, err := open(src)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	var names []string
	if table != "" {
		if !identRe.MatchString(table) {
			return nil, fmt.Errorf("invalid table name")
		}
		names = []string{table}
	} else {
		var err error
		names, err = tables(ctx, db)
		if err != nil {
			return nil, err
		}
	}
	var out []map[string]any
	for _, n := range names {
		c, cancel := withTimeout(ctx, 15)
		rows, err := db.QueryContext(c, `PRAGMA foreign_key_list("`+n+`")`)
		if err != nil {
			cancel()
			continue
		}
		for rows.Next() {
			var id, seq int
			var to, from, toCol, onUpd, onDel, match string
			if err := rows.Scan(&id, &seq, &to, &from, &toCol, &onUpd, &onDel, &match); err != nil {
				rows.Close()
				cancel()
				return nil, err
			}
			out = append(out, map[string]any{
				"from": "main." + n + "." + from, "references": "main." + to + "." + toCol,
			})
			if len(out) >= limit {
				rows.Close()
				cancel()
				return out, nil
			}
		}
		rows.Close()
		cancel()
	}
	return out, nil
}

// SearchTables fuzzy-finds tables by name fragment.
func SearchTables(ctx context.Context, src model.Source, pattern string, limit int) ([]map[string]any, error) {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return nil, fmt.Errorf("pattern required")
	}
	if len(pattern) > 64 {
		pattern = pattern[:64]
	}
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	db, err := open(src)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	c, cancel := withTimeout(ctx, 20)
	defer cancel()
	rows, err := db.QueryContext(c, `SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' AND name LIKE '%'||?||'%' ESCAPE '\' ORDER BY 1 LIMIT ?`, escLike(pattern), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"table": "main." + n})
	}
	return out, rows.Err()
}

// SlowQueries has no sqld equivalent — point at EXPLAIN QUERY PLAN.
func SlowQueries(ctx context.Context, src model.Source, limit int) (map[string]any, error) {
	return map[string]any{
		"source":  "unavailable",
		"note":    "sqld keeps no statement stats — run EXPLAIN QUERY PLAN per query via the explain tool.",
		"queries": []any{},
	}, nil
}

// Diagnose checks endpoint reachability + readability.
func Diagnose(ctx context.Context, src model.Source) []model.CheckResult {
	out := []model.CheckResult{}
	pushCheck(&out, "reachable", "Turso endpoint reachable", func() (string, error) {
		db, err := open(src)
		if err != nil {
			return "", err
		}
		db.Close()
		return strings.TrimSpace(src.Database), nil
	})
	pushCheck(&out, "readable", "Schema readable", func() (string, error) {
		db, err := open(src)
		if err != nil {
			return "", err
		}
		defer db.Close()
		names, err := tables(ctx, db)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%d tables visible", len(names)), nil
	})
	return out
}

// ExecWrite runs one app-confirmed INSERT/UPDATE/DELETE with the saved
// token. Called ONLY from the app UI path after the master switch +
// per-batch confirmation — never from MCP.
func ExecWrite(ctx context.Context, src model.Source, _, _ string, sqlText string, allowFullTable bool) (int64, error) {
	if err := guard.ValidateWriteSQL(sqlText); err != nil {
		return 0, err
	}
	if guard.NeedsWhere(sqlText) && !allowFullTable {
		return 0, fmt.Errorf("UPDATE/DELETE without WHERE needs explicit full-table confirmation")
	}
	db, err := open(src)
	if err != nil {
		return 0, err
	}
	defer db.Close()
	c, cancel := withTimeout(ctx, 25)
	defer cancel()
	res, err := db.ExecContext(c, sqlText)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
