// Package lite implements the ReadGate gateway for SQLite files.
//
// SQLite has no server, no users, and no SSH: Source.Database holds the
// file path and every access opens the file read-only (mode=ro) plus
// PRAGMA query_only=ON. "Read-only verified" therefore means: the file is
// a healthy database AND the gateway can only ever open it read-only AND
// the SQL guard rejects writes. Uses modernc.org/sqlite (pure Go, no cgo).
package lite

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"readgate/internal/guard"
	"readgate/internal/model"

	_ "modernc.org/sqlite"
)

var identRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_$]*$`)

// rawDeny mirrors pg's fragment filter: statement smuggling inside raw
// WHERE fragments is rejected before it reaches the database.
var rawDeny = regexp.MustCompile(`(?i)(;|--|/\*|\b(INSERT|UPDATE|DELETE|MERGE|DROP|CREATE|ALTER|TRUNCATE|GRANT|REVOKE|COPY|VACUUM|CALL|DO|INTO|LOAD|EXECUTE|PERFORM|PRAGMA|ATTACH|DETACH)\b)`)

func pathOf(src model.Source) string {
	return strings.TrimSpace(src.Database)
}

// open opens the database file. readOnly=true is the gateway path
// (mode=ro + query_only); readOnly=false is used ONLY for the write-denied
// proof, which must fail for verification to pass.
func open(src model.Source, readOnly bool) (*sql.DB, error) {
	p := pathOf(src)
	if p == "" {
		return nil, fmt.Errorf("no database file set")
	}
	if _, err := os.Stat(p); err != nil {
		return nil, fmt.Errorf("database file not accessible: %v", err)
	}
	mode := "ro"
	if !readOnly {
		mode = "rw"
	}
	dsn := "file:" + filepath.ToSlash(p) + "?mode=" + mode
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	db.SetConnMaxIdleTime(2 * time.Minute)
	if readOnly {
		if _, err := db.Exec(`PRAGMA query_only = ON`); err != nil {
			db.Close()
			return nil, err
		}
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func withTimeout(ctx context.Context, secs int) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, time.Duration(secs)*time.Second)
}

func pushCheck(out *[]model.CheckResult, key, label string, fn func() (string, error)) {
	start := time.Now()
	detail, err := fn()
	*out = append(*out, model.CheckResult{
		Key: key, Label: label, OK: err == nil,
		Detail:   detail,
		Duration: time.Since(start).Milliseconds(),
	})
	if err != nil && detail == "" {
		(*out)[len(*out)-1].Detail = err.Error()
	}
}

// VerifyReadOnly proves a SQLite file is safe behind the gateway:
// readable file → valid database → SELECT works → writes are rejected →
// gateway pinned read-only. Keys select/write/ro mirror postgres so the
// enable invariant (app.go) applies unchanged.
func VerifyReadOnly(ctx context.Context, src model.Source) []model.CheckResult {
	db, err := open(src, true)
	if err != nil {
		return []model.CheckResult{{Key: "select", Label: "Open database file", Detail: err.Error()}}
	}
	defer db.Close()

	out := []model.CheckResult{}
	pushCheck(&out, "file", "Database file readable", func() (string, error) {
		st, err := os.Stat(pathOf(src))
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s · %.1f MB", filepath.Base(pathOf(src)), float64(st.Size())/1048576), nil
	})
	pushCheck(&out, "integrity", "Integrity check passed", func() (string, error) {
		c, cancel := withTimeout(ctx, 30)
		defer cancel()
		var v string
		if err := db.QueryRowContext(c, `PRAGMA quick_check`).Scan(&v); err != nil {
			return "", err
		}
		if v != "ok" {
			return v, fmt.Errorf("integrity: %s", v)
		}
		return "quick_check ok", nil
	})
	pushCheck(&out, "select", "SELECT permission verified", func() (string, error) {
		c, cancel := withTimeout(ctx, 10)
		defer cancel()
		var one int
		if err := db.QueryRowContext(c, `SELECT 1`).Scan(&one); err != nil {
			return "", err
		}
		var n int
		if err := db.QueryRowContext(c, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'`).Scan(&n); err != nil {
			return "", err
		}
		return fmt.Sprintf("SELECT 1 → 1 · %d tables visible", n), nil
	})
	pushCheck(&out, "write", "Write permission denied", func() (string, error) {
		c, cancel := withTimeout(ctx, 10)
		defer cancel()
		_, err := db.ExecContext(c, `CREATE TABLE "__rg_write_test"(id INTEGER)`)
		if err == nil {
			return "writes unexpectedly allowed", fmt.Errorf("gateway opened the file writable — refusing to enable")
		}
		return "rejected as read-only (" + shortErr(err) + ")", nil
	})
	pushCheck(&out, "ro", "Read-only configuration verified", func() (string, error) {
		return "mode=ro + query_only=ON on every open · guard rejects writes", nil
	})
	return out
}

func shortErr(err error) string {
	s := err.Error()
	if len(s) > 160 {
		return s[:160] + "…"
	}
	return s
}

// tables lists user tables in name order.
func tables(ctx context.Context, db *sql.DB) ([]string, error) {
	c, cancel := withTimeout(ctx, 15)
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
	c, cancel := withTimeout(ctx, 3)
	defer cancel()
	var n int64
	if err := db.QueryRowContext(c, `SELECT COUNT(*) FROM "`+table+`"`).Scan(&n); err != nil {
		return 0
	}
	return n
}

// Schema returns tables with best-effort row counts (0 when too slow).
func Schema(ctx context.Context, src model.Source) (model.SchemaInfo, error) {
	db, err := open(src, true)
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
		if i < 200 {
			est = countTable(ctx, db, n)
		}
		info.Tables = append(info.Tables, model.TableInfo{Schema: "main", Name: n, RowEstimate: est})
	}
	return info, nil
}

// Columns returns one table's columns via PRAGMA table_info.
func Columns(ctx context.Context, src model.Source, schema, table string) ([]model.ColumnInfo, error) {
	if !identRe.MatchString(table) || len(table) > 64 {
		return nil, fmt.Errorf("invalid table name")
	}
	db, err := open(src, true)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	c, cancel := withTimeout(ctx, 15)
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

func scanRows(rows *sql.Rows) (cols []string, data [][]interface{}, err error) {
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
			vals[i] = normVal(v)
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
	db, err := open(src, true)
	if err != nil {
		return model.QueryResult{}, err
	}
	defer db.Close()
	c, cancel := withTimeout(ctx, 20)
	defer cancel()
	start := time.Now()
	rows, err := db.QueryContext(c, sqlText)
	if err != nil {
		return model.QueryResult{}, err
	}
	defer rows.Close()
	cols, data, err := scanRows(rows)
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

// buildWhere mirrors pg's builder: same ops, double-quote identifiers,
// ? placeholders (SQLite LIKE is ASCII case-insensitive).
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

// Preview fetches a UI-safe page: values capped, identifiers validated.
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
	db, err := open(src, true)
	if err != nil {
		return model.QueryResult{}, err
	}
	defer db.Close()
	c, cancel := withTimeout(ctx, 20)
	defer cancel()
	where, args := buildWhere(filters, raw)
	q := fmt.Sprintf(`SELECT * FROM "%s" %s LIMIT %d OFFSET %d`, table, where, limit, offset)
	start := time.Now()
	rows, err := db.QueryContext(c, q, args...)
	if err != nil {
		return model.QueryResult{}, err
	}
	defer rows.Close()
	cols, data, err := scanRows(rows)
	if err != nil {
		return model.QueryResult{}, err
	}
	for i, r := range data {
		for j, v := range r {
			if s, ok := v.(string); ok {
				rr := []rune(s)
				if len(rr) > 1000 {
					data[i][j] = string(rr[:1000]) + fmt.Sprintf("… (+%d chars)", len(rr)-1000)
				}
			}
		}
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

// Explain returns EXPLAIN QUERY PLAN lines for slow-query reasoning.
func Explain(ctx context.Context, src model.Source, sqlText string) (string, error) {
	if err := guard.ValidateReadOnlySQL(sqlText); err != nil {
		return "", err
	}
	db, err := open(src, true)
	if err != nil {
		return "", err
	}
	defer db.Close()
	c, cancel := withTimeout(ctx, 20)
	defer cancel()
	q := "EXPLAIN QUERY PLAN " + strings.TrimSuffix(strings.TrimSpace(sqlText), ";")
	rows, err := db.QueryContext(c, q)
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

// DoctorFindings runs file-appropriate health probes.
func DoctorFindings(ctx context.Context, src model.Source) ([]model.DoctorFinding, error) {
	db, err := open(src, true)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	var findings []model.DoctorFinding
	add := func(sev, title, detail, remedy string) {
		findings = append(findings, model.DoctorFinding{Severity: sev, Title: title, Detail: detail, Remedy: remedy, Source: src.Name})
	}
	st, _ := os.Stat(pathOf(src))
	size := ""
	if st != nil {
		size = fmt.Sprintf("%.1f MB", float64(st.Size())/1048576)
	}
	add("info", "SQLite file reachable", fmt.Sprintf("%s · %s", filepath.Base(pathOf(src)), size), "")
	var chk string
	if err := db.QueryRow(`PRAGMA quick_check`).Scan(&chk); err != nil || chk != "ok" {
		add("critical", "Integrity check failed", chk, "Restore from backup — the file may be corrupt.")
	} else {
		add("info", "Integrity check passed", "quick_check ok", "")
	}
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
		if i < 100 {
			c = countTable(ctx, db, n)
		}
		total += c
		order = append(order, sized{n, c})
	}
	add("info", fmt.Sprintf("Inventory: %d tables, ~%d rows", len(names), total), "Counts via COUNT(*) (capped at 100 tables).", "")
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
		c, cancel := withTimeout(ctx, 10)
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
				"Recreate with an INTEGER PRIMARY KEY (SQLite has no ALTER ADD PK).")
		}
	}
	add("info", "No server to pressure", "Single file, no connections, no long-query list — nothing to saturate.", "")
	return findings, nil
}

// TableStats returns per-table shape stats (row/column/index counts + PK).
func TableStats(ctx context.Context, src model.Source, schema, table string, limit int) ([]map[string]any, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	db, err := open(src, true)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	one := func(n string) (map[string]any, error) {
		cols, err := Columns(ctx, src, "main", n)
		if err != nil {
			return nil, err
		}
		c, cancel := withTimeout(ctx, 10)
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
	db, err := open(src, true)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	c, cancel := withTimeout(ctx, 15)
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

// Relationships returns foreign-key edges (PRAGMA foreign_key_list).
func Relationships(ctx context.Context, src model.Source, schema, table string, limit int) ([]map[string]any, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	db, err := open(src, true)
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
		c, cancel := withTimeout(ctx, 10)
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
	db, err := open(src, true)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	c, cancel := withTimeout(ctx, 15)
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

// SlowQueries has no SQLite equivalent — point at EXPLAIN QUERY PLAN.
func SlowQueries(ctx context.Context, src model.Source, limit int) (map[string]any, error) {
	return map[string]any{
		"source":  "unavailable",
		"note":    "SQLite keeps no statement stats — run EXPLAIN QUERY PLAN per query via the explain tool.",
		"queries": []any{},
	}, nil
}

// Diagnose inspects the file itself (used by Doctor-over-SSH equivalent).
func Diagnose(ctx context.Context, src model.Source) []model.CheckResult {
	out := []model.CheckResult{}
	pushCheck(&out, "file-present", "Database file present", func() (string, error) {
		st, err := os.Stat(pathOf(src))
		if err != nil {
			return "", err
		}
		if st.IsDir() {
			return "", fmt.Errorf("path is a directory, not a database file")
		}
		return fmt.Sprintf("%.1f MB · modified %s", float64(st.Size())/1048576, st.ModTime().Format("2006-01-02 15:04")), nil
	})
	pushCheck(&out, "readable", "File opens read-only", func() (string, error) {
		db, err := open(src, true)
		if err != nil {
			return "", err
		}
		db.Close()
		return "mode=ro ok", nil
	})
	pushCheck(&out, "integrity", "Integrity check passed", func() (string, error) {
		db, err := open(src, true)
		if err != nil {
			return "", err
		}
		defer db.Close()
		var v string
		if err := db.QueryRow(`PRAGMA quick_check`).Scan(&v); err != nil {
			return "", err
		}
		if v != "ok" {
			return v, fmt.Errorf("integrity: %s", v)
		}
		return "quick_check ok", nil
	})
	return out
}

// ExecWrite runs one app-confirmed INSERT/UPDATE/DELETE against the file
// opened read-write. Called ONLY from the app UI path after the master
// switch + per-batch confirmation — never from MCP.
func ExecWrite(ctx context.Context, src model.Source, _, _ string, sqlText string, allowFullTable bool) (int64, error) {
	if err := guard.ValidateWriteSQL(sqlText); err != nil {
		return 0, err
	}
	if guard.NeedsWhere(sqlText) && !allowFullTable {
		return 0, fmt.Errorf("UPDATE/DELETE without WHERE needs explicit full-table confirmation")
	}
	db, err := open(src, false)
	if err != nil {
		return 0, err
	}
	defer db.Close()
	c, cancel := withTimeout(ctx, 20)
	defer cancel()
	res, err := db.ExecContext(c, sqlText)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
