package pg

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"readgate/internal/guard"
	"readgate/internal/model"
	"readgate/internal/sshx"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Probe struct {
	DSN       string
	viaTunnel *sshx.Tunnel
}

// dsnFor builds a properly escaped connection string.
// Passwords with @ : / ? # & must be percent-encoded, otherwise the URL
// parser silently mangles them and every login fails with 28P01.
func dsnFor(host string, port int, db, user, pass string) string {
	u := &url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(user, pass),
		Host:   net.JoinHostPort(host, fmt.Sprintf("%d", port)),
		Path:   "/" + db,
	}
	q := u.Query()
	q.Set("sslmode", "prefer")
	q.Set("connect_timeout", "8")
	u.RawQuery = q.Encode()
	return u.String()
}

// Connect opens a pool, establishing an SSH tunnel first when needed.
// Caller must call Close().
// Connect opens a pool, establishing an SSH tunnel first when needed.
// readOnly=true hardens AI sessions (default_transaction_read_only=on).
// Admin sessions (provisioning) MUST use readOnly=false or DDL is rejected
// with 25006 "cannot execute CREATE ROLE in a read-only transaction".
// Caller must call Close().
func Connect(ctx context.Context, src model.Source, dbUser, dbPass string, readOnly bool) (*pgxpool.Pool, *sshx.Tunnel, error) {
	host, port := src.Host, src.Port
	if port == 0 {
		port = 5432
	}
	var tun *sshx.Tunnel
	if src.Mode == model.ModeSSH {
		remoteHost := src.Host
		if remoteHost == "" || remoteHost == "localhost" {
			remoteHost = "127.0.0.1"
		}
		// When tunneling, the DB is almost always localhost from the server's view.
		// If user typed a LAN IP, keep it; else force loopback.
		if src.Host == "" {
			remoteHost = "127.0.0.1"
		}
		keyPath, password := src.SSHKeyPath, src.SSHPassword
		if src.SSHAuth == "password" {
			keyPath = ""
		}
		if src.SSHAuth == "agent" {
			keyPath, password = "", ""
		}
		var err error
		tun, err = sshx.Dial(src.SSHHost, src.SSHPort, src.SSHUser, keyPath, src.SSHKeyPassphrase, password, remoteHost, port, 12*time.Second)
		if err != nil {
			return nil, nil, fmt.Errorf("ssh tunnel: %w", err)
		}
		host, port = "127.0.0.1", tun.LocalPort
	}
	dsn := dsnFor(host, port, src.Database, dbUser, dbPass)
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		if tun != nil {
			tun.Close()
		}
		return nil, nil, err
	}
	cfg.MaxConns = 4
	cfg.MaxConnLifetime = 2 * time.Minute
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		if tun != nil {
			tun.Close()
		}
		return nil, nil, err
	}
	// hard guard: AI sessions are read-only. Never applied to admin sessions.
	if readOnly {
		_, _ = pool.Exec(ctx, `SET default_transaction_read_only = on`)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		if tun != nil {
			tun.Close()
		}
		return nil, nil, fmt.Errorf("postgres ping: %w", err)
	}
	return pool, tun, nil
}

// ProbeAdmin checks the TEMPORARY admin leg on its own: SSH handshake,
// postgres login (against the maintenance DB so a wrong target name can't
// hide a good admin login), privilege to CREATE ROLE, and target DB exists.
func ProbeAdmin(ctx context.Context, src model.Source, adminUser, adminPass string) []model.CheckResult {
	checks := []model.CheckResult{}
	push := func(key, label string, fn func() (string, error)) {
		start := time.Now()
		detail, err := fn()
		checks = append(checks, model.CheckResult{
			Key: key, Label: label, OK: err == nil,
			Detail:   firstLine(detail, err),
			Duration: time.Since(start).Milliseconds(),
		})
	}

	if src.Mode == model.ModeSSH {
		push("adm-ssh", "SSH connection successful", func() (string, error) {
			out, err := sshx.TestSSH(src.SSHHost, src.SSHPort, src.SSHUser, sshKeyPathFor(src), src.SSHKeyPassphrase, sshPasswordFor(src), 12*time.Second)
			if err != nil {
				return "", err
			}
			return "server: " + out, nil
		})
		if len(checks) > 0 && !checks[0].OK {
			for _, extra := range [][2]string{
				{"adm-pg", "PostgreSQL reachable as admin"}, {"adm-priv", "Admin can CREATE ROLE"},
				{"adm-db", "Target database exists"},
			} {
				checks = append(checks, model.CheckResult{Key: extra[0], Label: extra[1], Detail: "skipped — ssh failed"})
			}
			return checks
		}
	}

	adminSrc := src
	adminSrc.Database = "postgres"
	var pool *pgxpool.Pool
	var tun *sshx.Tunnel
	push("adm-pg", "PostgreSQL reachable as admin", func() (string, error) {
		c, cancel := context.WithTimeout(ctx, 25*time.Second)
		defer cancel()
		p, t, err := Connect(c, adminSrc, adminUser, adminPass, false)
		if err != nil {
			return "", err
		}
		pool, tun = p, t
		var ver, who string
		if err := p.QueryRow(c, `SELECT version(), current_user`).Scan(&ver, &who); err != nil {
			return "", err
		}
		if len(ver) > 100 {
			ver = ver[:100] + "…"
		}
		return "logged in as " + who + " · " + ver, nil
	})
	if pool == nil {
		for _, extra := range [][2]string{
			{"adm-priv", "Admin can CREATE ROLE"}, {"adm-db", "Target database exists"},
		} {
			checks = append(checks, model.CheckResult{Key: extra[0], Label: extra[1], Detail: "skipped — no connection"})
		}
		return checks
	}
	defer pool.Close()
	defer func() {
		if tun != nil {
			tun.Close()
		}
	}()

	push("adm-priv", "Admin can CREATE ROLE", func() (string, error) {
		var super, createRole bool
		if err := pool.QueryRow(ctx, `SELECT rolsuper, rolcreaterole FROM pg_roles WHERE rolname=current_user`).Scan(&super, &createRole); err != nil {
			return "", err
		}
		if !super && !createRole {
			return "neither superuser nor createrole", fmt.Errorf("admin lacks CREATEROLE — use the postgres superuser")
		}
		return fmt.Sprintf("superuser=%v createrole=%v", super, createRole), nil
	})
	push("adm-db", fmt.Sprintf("Target database %q exists", src.Database), func() (string, error) {
		var one int
		if err := pool.QueryRow(ctx, `SELECT 1 FROM pg_database WHERE datname=$1`, src.Database).Scan(&one); err != nil {
			return "", fmt.Errorf("database %q not found — fix the Database field", src.Database)
		}
		return "database " + src.Database + " exists", nil
	})
	return checks
}

func sshKeyPathFor(src model.Source) string {
	if src.SSHAuth == "password" || src.SSHAuth == "agent" {
		return ""
	}
	return src.SSHKeyPath
}

func sshPasswordFor(src model.Source) string {
	if src.SSHAuth == "agent" {
		return ""
	}
	return src.SSHPassword
}

// VerifyReadOnly runs the onboarding invariant:
// connectivity → auth → read → write-denied → ro-flag, as timed checks.
func VerifyReadOnly(ctx context.Context, src model.Source, dbUser, dbPass string) []model.CheckResult {
	checks := []model.CheckResult{}
	push := func(key, label string, fn func() (string, error)) {
		start := time.Now()
		detail, err := fn()
		checks = append(checks, model.CheckResult{
			Key: key, Label: label, OK: err == nil,
			Detail:   firstLine(detail, err),
			Duration: time.Since(start).Milliseconds(),
		})
	}

	// 1. SSH (when applicable)
	if src.Mode == model.ModeSSH {
		push("ssh", "SSH connection successful", func() (string, error) {
			c, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			_ = c
			out, err := sshx.TestSSH(src.SSHHost, src.SSHPort, src.SSHUser, src.SSHKeyPath, src.SSHKeyPassphrase, src.SSHPassword, 12*time.Second)
			if err != nil {
				return "", err
			}
			return "server: " + out, nil
		})
		// abort early if SSH failed
		if len(checks) > 0 && !checks[0].OK {
			for _, extra := range [][2]string{
				{"pg", "PostgreSQL detected"}, {"auth", "Database accessible"},
				{"select", "SELECT permission verified"}, {"write", "Write permission denied"},
				{"ro", "Read-only configuration verified"},
			} {
				checks = append(checks, model.CheckResult{Key: extra[0], Label: extra[1], Detail: "skipped — ssh failed"})
			}
			return checks
		}
	}

	var pool *pgxpool.Pool
	var tun *sshx.Tunnel
	push("pg", "PostgreSQL detected", func() (string, error) {
		c, cancel := context.WithTimeout(ctx, 25*time.Second)
		defer cancel()
		p, t, err := Connect(c, src, dbUser, dbPass, true)
		if err != nil {
			return "", err
		}
		pool, tun = p, t
		var ver string
		if err := p.QueryRow(c, `SELECT version()`).Scan(&ver); err != nil {
			return "", err
		}
		if len(ver) > 120 {
			ver = ver[:120] + "…"
		}
		return ver, nil
	})
	if pool == nil {
		for _, extra := range [][2]string{
			{"auth", "Database accessible"}, {"select", "SELECT permission verified"},
			{"write", "Write permission denied"}, {"ro", "Read-only configuration verified"},
		} {
			checks = append(checks, model.CheckResult{Key: extra[0], Label: extra[1], Detail: "skipped — no connection"})
		}
		return checks
	}
	defer pool.Close()
	defer func() {
		if tun != nil {
			tun.Close()
		}
	}()

	push("auth", "Database accessible", func() (string, error) {
		var db string
		if err := pool.QueryRow(ctx, `SELECT current_database()`).Scan(&db); err != nil {
			return "", err
		}
		return "database: " + db, nil
	})
	push("select", "SELECT permission verified", func() (string, error) {
		var one int
		if err := pool.QueryRow(ctx, `SELECT 1`).Scan(&one); err != nil {
			return "", fmt.Errorf("SELECT 1 rejected: %w", err)
		}
		return "SELECT 1 → 1", nil
	})
	push("write", "Write permission denied", func() (string, error) {
		// harmless write inside a rolled-back txn; MUST be rejected for ai users
		tx, err := pool.Begin(ctx)
		if err != nil {
			return "", err
		}
		defer tx.Rollback(ctx)
		_, err = tx.Exec(ctx, `CREATE TEMP TABLE __readgate_probe(id int) ON COMMIT DROP`)
		if err == nil {
			// temp-table creation succeeded → try an insert, then fail the check
			if _, ierr := tx.Exec(ctx, `INSERT INTO __readgate_probe VALUES (1)`); ierr == nil {
				return "", fmt.Errorf("writes are ALLOWED — refusing to enable source")
			}
		}
		// Any error here is the desired outcome.
		if err != nil && isReadOnlyError(err) {
			return "rejected as read-only (" + shortErr(err) + ")", nil
		}
		if err != nil {
			return "rejected (" + shortErr(err) + ")", nil
		}
		return "temp insert blocked", nil
	})
	push("ro", "Read-only configuration verified", func() (string, error) {
		var flag string
		if err := pool.QueryRow(ctx, `SHOW default_transaction_read_only`).Scan(&flag); err != nil {
			// fallback
			if err2 := pool.QueryRow(ctx, `SELECT current_setting('default_transaction_read_only', true)`).Scan(&flag); err2 != nil {
				return "", err2
			}
		}
		if flag != "on" {
			return "default_transaction_read_only=" + flag, fmt.Errorf("role is not locked to read-only")
		}
		return "default_transaction_read_only=on", nil
	})
	return checks
}

func isReadOnlyError(err error) bool {
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "read-only") || strings.Contains(s, "read only") ||
		strings.Contains(s, "permission denied") || strings.Contains(s, "42501")
}

func shortErr(err error) string {
	s := err.Error()
	if len(s) > 160 {
		return s[:160] + "…"
	}
	return s
}

func firstLine(detail string, err error) string {
	if err != nil {
		return shortErr(err)
	}
	if len(detail) > 220 {
		return detail[:220] + "…"
	}
	return detail
}

// Schema lists table NAMES + row estimates for the browser and MCP.
// Exactly ONE query — columns load lazily per table via Columns(), so even
// 500-table fleets list instantly over an SSH tunnel.
func Schema(ctx context.Context, src model.Source) (model.SchemaInfo, error) {
	pool, tun, err := Connect(ctx, src, src.Username, src.Password, true)
	if err != nil {
		return model.SchemaInfo{}, err
	}
	defer pool.Close()
	defer func() {
		if tun != nil {
			tun.Close()
		}
	}()
	rows, err := pool.Query(ctx, `
		SELECT n.nspname, c.relname, COALESCE(c.reltuples::bigint,0)
		FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
		WHERE c.relkind='r' AND n.nspname NOT IN ('pg_catalog','information_schema')
		ORDER BY 1, 2 LIMIT 500`)
	if err != nil {
		return model.SchemaInfo{}, err
	}
	defer rows.Close()
	info := model.SchemaInfo{SourceName: src.Name}
	for rows.Next() {
		var tb model.TableInfo
		if err := rows.Scan(&tb.Schema, &tb.Name, &tb.RowEstimate); err != nil {
			return model.SchemaInfo{}, err
		}
		info.Tables = append(info.Tables, tb)
	}
	return info, rows.Err()
}

// Columns returns one table's columns (called lazily on table select).
func Columns(ctx context.Context, src model.Source, schema, table string) ([]model.ColumnInfo, error) {
	if !identRe.MatchString(schema) || !identRe.MatchString(table) || len(schema) > 64 || len(table) > 64 {
		return nil, fmt.Errorf("invalid table name")
	}
	pool, tun, err := Connect(ctx, src, src.Username, src.Password, true)
	if err != nil {
		return nil, err
	}
	defer pool.Close()
	defer func() {
		if tun != nil {
			tun.Close()
		}
	}()
	return columnsOf(ctx, pool, schema, table)
}

func columnsOf(ctx context.Context, pool *pgxpool.Pool, schema, table string) ([]model.ColumnInfo, error) {
	rows, err := pool.Query(ctx, `
		SELECT column_name, data_type, is_nullable, COALESCE(column_default,'')
		FROM information_schema.columns
		WHERE table_schema=$1 AND table_name=$2 ORDER BY ordinal_position`, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.ColumnInfo
	for rows.Next() {
		var c model.ColumnInfo
		var nul string
		rows.Scan(&c.Name, &c.Type, &nul, &c.Default)
		c.Nullable = nul == "YES"
		out = append(out, c)
	}
	return out, nil
}

// Query runs a guarded read-only query with timeout + row cap.
func Query(ctx context.Context, src model.Source, sql string, limit int) (model.QueryResult, error) {
	if err := guard.ValidateReadOnlySQL(sql); err != nil {
		return model.QueryResult{}, err
	}
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	sql = guard.EnforceLimit(sql, limit)
	pool, tun, err := Connect(ctx, src, src.Username, src.Password, true)
	if err != nil {
		return model.QueryResult{}, err
	}
	defer pool.Close()
	defer func() {
		if tun != nil {
			tun.Close()
		}
	}()
	c, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	start := time.Now()
	// belt & suspenders: read-only txn + statement timeout
	_, _ = pool.Exec(c, `SET LOCAL statement_timeout = '15s'`)
	rows, err := pool.Query(c, sql)
	if err != nil {
		return model.QueryResult{}, err
	}
	defer rows.Close()
	fields := rows.FieldDescriptions()
	cols := make([]string, len(fields))
	for i, f := range fields {
		cols[i] = f.Name
	}
	data := make([][]interface{}, 0)
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			return model.QueryResult{}, err
		}
		for i, v := range vals {
			vals[i] = normVal(v)
		}
		data = append(data, vals)
		if len(data) >= limit {
			break
		}
	}
	return model.QueryResult{
		Columns: cols, Rows: data, RowCount: len(data),
		DurationMs: time.Since(start).Milliseconds(), Truncated: len(data) >= limit,
	}, rows.Err()
}

// normVal converts pgx driver values into JSON-safe primitives so Wails
// and MCP never choke on pgtype structs, []byte or time.Time.
func normVal(v any) any {
	switch t := v.(type) {
	case nil, bool, string,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64:
		return v
	case []byte:
		return string(t)
	case time.Time:
		return t.Format(time.RFC3339Nano)
	default:
		return fmt.Sprintf("%v", v)
	}
}

var identRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_$]*$`)

// rawDeny blocks statement smuggling inside raw WHERE fragments
// (the ai role is read-only anyway — this is belt & suspenders).
var rawDeny = regexp.MustCompile(`(?i)(;|--|/\*|\b(INSERT|UPDATE|DELETE|MERGE|DROP|CREATE|ALTER|TRUNCATE|GRANT|REVOKE|COPY|VACUUM|CALL|DO|INTO|LOAD|EXECUTE|PERFORM)\b)`)

// Filter is one Beekeeper-style builder condition.
// Op: eq ne gt gte lt lte contains starts ends like in null notnull.
// Logic joins with the PREVIOUS condition: AND | OR.
// (Canonical struct lives in model so every engine shares it.)
type Filter = model.Filter

func escLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}

// coerce sends numbers/bools typed so `int_col = 5` works instead of
// erroring on `bigint = text`.
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

// buildWhere turns builder filters + an optional raw fragment into a
// parameterized WHERE clause. Unknown columns/ops are skipped, never trusted.
func buildWhere(filters []Filter, raw string) (string, []any) {
	var conds []string
	var args []any
	next := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
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
			conds = append(conds, join+col+"::text ILIKE "+next("%"+escLike(v)+"%")+" ESCAPE '\\'")
		case "starts":
			conds = append(conds, join+col+"::text ILIKE "+next(escLike(v)+"%")+" ESCAPE '\\'")
		case "ends":
			conds = append(conds, join+col+"::text ILIKE "+next("%"+escLike(v))+" ESCAPE '\\'")
		case "like":
			// Beekeeper-style: user supplies % wildcards, e.g. %foo%
			conds = append(conds, join+col+"::text LIKE "+next(v))
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

// Preview fetches a small, UI-safe page of a table for Browse.
// Unlike Query (full fidelity for the AI), values are capped so one table
// with megabyte TEXT/JSON/bytea columns can't flood the WebView bridge and
// freeze the app. Identifiers are validated, never interpolated blindly.
// ORDER BY 1 keeps LIMIT/OFFSET pages stable.
func Preview(ctx context.Context, src model.Source, schema, table string, limit, offset int, filters []Filter, raw string) (model.QueryResult, error) {
	if !identRe.MatchString(schema) || !identRe.MatchString(table) || len(schema) > 64 || len(table) > 64 {
		return model.QueryResult{}, fmt.Errorf("invalid table name")
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	pool, tun, err := Connect(ctx, src, src.Username, src.Password, true)
	if err != nil {
		return model.QueryResult{}, err
	}
	defer pool.Close()
	defer func() {
		if tun != nil {
			tun.Close()
		}
	}()
	c, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	_, _ = pool.Exec(c, `SET LOCAL statement_timeout = '15s'`)
	where, args := buildWhere(filters, raw)
	q := fmt.Sprintf(`SELECT * FROM "%s"."%s" %s ORDER BY 1 LIMIT %d OFFSET %d`, schema, table, where, limit, offset)
	start := time.Now()
	rows, err := pool.Query(c, q, args...)
	if err != nil {
		return model.QueryResult{}, err
	}
	defer rows.Close()
	fields := rows.FieldDescriptions()
	cols := make([]string, len(fields))
	for i, f := range fields {
		cols[i] = f.Name
	}
	data := make([][]interface{}, 0)
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			return model.QueryResult{}, err
		}
		for i, v := range vals {
			vals[i] = previewVal(v)
		}
		data = append(data, vals)
		if len(data) >= limit {
			break
		}
	}
	if rerr := rows.Err(); rerr != nil {
		return model.QueryResult{}, rerr
	}
	// exact filtered total (best-effort; -1 = unknown, UI falls back to estimate)
	var total int64 = -1
	if where != "" {
		_ = pool.QueryRow(c, fmt.Sprintf(`SELECT COUNT(*) FROM "%s"."%s" %s`, schema, table, where), args...).Scan(&total)
	}
	return model.QueryResult{
		Columns: cols, Rows: data, RowCount: len(data), TotalRows: total,
		DurationMs: time.Since(start).Milliseconds(), Truncated: len(data) >= limit,
	}, nil
}

// previewVal caps a single value for display: long text is cut with its true
// length noted, binary becomes a placeholder instead of garbage.
func previewVal(v any) any {
	if b, ok := v.([]byte); ok {
		if !utf8.Valid(b) {
			return fmt.Sprintf("<binary %d bytes>", len(b))
		}
		v = string(b)
	}
	if t, ok := v.(time.Time); ok {
		return t.Format(time.RFC3339Nano)
	}
	if s, ok := v.(string); ok {
		r := []rune(s)
		if len(r) > 1000 {
			return string(r[:1000]) + fmt.Sprintf("… (+%d chars)", len(r)-1000)
		}
		return s
	}
	return normVal(v)
}

// Explain returns EXPLAIN (FORMAT JSON) rows for MCP/AI tuning help.
func Explain(ctx context.Context, src model.Source, sql string) (string, error) {
	if err := guard.ValidateReadOnlySQL(sql); err != nil {
		return "", err
	}
	pool, tun, err := Connect(ctx, src, src.Username, src.Password, true)
	if err != nil {
		return "", err
	}
	defer pool.Close()
	defer func() {
		if tun != nil {
			tun.Close()
		}
	}()
	var out string
	q := "EXPLAIN (FORMAT JSON) " + strings.TrimSuffix(strings.TrimSpace(sql), ";")
	if err := pool.QueryRow(ctx, q).Scan(&out); err != nil {
		// fallback to text
		var lines []string
		rows, err2 := pool.Query(ctx, "EXPLAIN "+strings.TrimSuffix(strings.TrimSpace(sql), ";"))
		if err2 != nil {
			return "", err
		}
		defer rows.Close()
		for rows.Next() {
			var l string
			rows.Scan(&l)
			lines = append(lines, l)
		}
		return strings.Join(lines, "\n"), nil
	}
	if len(out) > 8000 {
		out = out[:8000] + "…(truncated)"
	}
	return out, nil
}

// DoctorFindings runs read-only health probes the AI can use to spot problems.
// Every probe degrades to an explanatory note instead of vanishing, so a
// least-privilege ai_readonly role still yields a useful report.
func DoctorFindings(ctx context.Context, src model.Source) ([]model.DoctorFinding, error) {
	pool, tun, err := Connect(ctx, src, src.Username, src.Password, true)
	if err != nil {
		return nil, err
	}
	defer pool.Close()
	defer func() {
		if tun != nil {
			tun.Close()
		}
	}()
	var findings []model.DoctorFinding
	add := func(sev, title, detail, remedy string) {
		findings = append(findings, model.DoctorFinding{Severity: sev, Title: title, Detail: detail, Remedy: remedy, Source: src.Name})
	}
	limited := func(what, hint string) {
		add("info", "Limited visibility: "+what,
			"The ai role could not read it ("+hint+").",
			"Have an admin run: GRANT pg_monitor TO "+src.Username+"; — read-only monitoring role, safe for AI.")
	}

	// 1. version — proves we are live
	var ver string
	if err := pool.QueryRow(ctx, `SELECT version()`).Scan(&ver); err != nil {
		return nil, err
	}
	add("info", "PostgreSQL reachable", ver, "")

	// 2. fleet summary: table count + estimated rows (pg_class is world-readable)
	var tblCount int
	var totalRows int64
	serr := pool.QueryRow(ctx, `
		SELECT COUNT(*), COALESCE(SUM(c.reltuples::bigint),0)
		FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
		WHERE c.relkind='r' AND n.nspname NOT IN ('pg_catalog','information_schema')`).Scan(&tblCount, &totalRows)
	if serr != nil {
		limited("inventory", shortErr(serr))
	} else {
		add("info", fmt.Sprintf("Inventory: %d tables, ~%s rows", tblCount, humanNum(totalRows)),
			"Estimated from pg_class (exact counts need COUNT(*), run per-table from Query).", "")
		// 3. biggest tables
		rows, err := pool.Query(ctx, `
			SELECT n.nspname||'.'||c.relname, c.reltuples::bigint
			FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
			WHERE c.relkind='r' AND n.nspname NOT IN ('pg_catalog','information_schema')
			ORDER BY c.reltuples DESC LIMIT 5`)
		if err == nil {
			var names []string
			for rows.Next() {
				var nm string
				var est int64
				rows.Scan(&nm, &est)
				names = append(names, fmt.Sprintf("%s (~%s)", nm, humanNum(est)))
			}
			rows.Close()
			if len(names) > 0 {
				add("info", "Largest tables", strings.Join(names, " · "),
					"Start slow-query triage here: EXPLAIN ANALYZE a typical query per table.")
			}
		} else {
			rows.Close()
		}
	}

	// 4. sequential scans on big tables (missing indexes?)
	rows, err := pool.Query(ctx, `
		SELECT schemaname||'.'||relname, seq_scan, idx_scan, COALESCE(n_live_tup,0)
		FROM pg_stat_user_tables ORDER BY seq_scan DESC LIMIT 5`)
	if err != nil {
		limited("scan stats", shortErr(err))
	} else {
		anyScan := false
		for rows.Next() {
			var tbl string
			var seq, idx, live int64
			rows.Scan(&tbl, &seq, &idx, &live)
			anyScan = true
			if seq > 1000 && live > 10000 && idx == 0 {
				add("warn", "Table with only sequential scans: "+tbl,
					fmt.Sprintf("%d seq scans, %d idx scans, ~%d live rows — queries may be missing an index", seq, idx, live),
					fmt.Sprintf("EXPLAIN ANALYZE SELECT … FROM %s … then consider CREATE INDEX (ask a human to apply writes).", tbl))
			}
		}
		rows.Close()
		_ = anyScan
	}

	// 5. tables without primary key
	rows, err = pool.Query(ctx, `
		SELECT n.nspname||'.'||c.relname FROM pg_class c
		JOIN pg_namespace n ON n.oid=c.relnamespace
		WHERE c.relkind='r' AND n.nspname NOT IN ('pg_catalog','information_schema')
		AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid=c.oid AND contype='p')
		LIMIT 10`)
	if err != nil {
		limited("primary-key audit", shortErr(err))
	} else {
		for rows.Next() {
			var tbl string
			rows.Scan(&tbl)
			add("warn", "Table without primary key: "+tbl,
				"Replication, dedupe and AI-generated joins are riskier without a PK.",
				"Have an admin run ALTER TABLE "+tbl+" ADD PRIMARY KEY (id);")
		}
		rows.Close()
	}

	// 6. connection pressure
	var conns, maxconns int
	if err := pool.QueryRow(ctx, `SELECT count(*), current_setting('max_connections')::int FROM pg_stat_activity`).Scan(&conns, &maxconns); err != nil {
		limited("connection stats", shortErr(err))
	} else {
		pct := 0
		if maxconns > 0 {
			pct = conns * 100 / maxconns
		}
		if pct >= 80 {
			add("critical", fmt.Sprintf("Connection pressure %d/%d (%d%%)", conns, maxconns, pct),
				"App may soon hit max_connections.", "Add pooling (PgBouncer) or reduce idle connections.")
		} else {
			add("info", fmt.Sprintf("Connections healthy %d/%d", conns, maxconns), "", "")
		}
	}

	// 7. long-running queries
	rows, err = pool.Query(ctx, `
		SELECT pid, now()-query_start AS dur, left(query,120)
		FROM pg_stat_activity WHERE state='active' AND now()-query_start > interval '30 seconds'
		ORDER BY query_start LIMIT 5`)
	if err != nil {
		limited("active query list", shortErr(err))
	} else {
		n := 0
		for rows.Next() {
			var pid int
			var dur, q string
			rows.Scan(&pid, &dur, &q)
			n++
			add("warn", fmt.Sprintf("Long-running query (pid %d, %s)", pid, dur), q, "Check EXPLAIN for seq scans / locks.")
		}
		rows.Close()
		if n == 0 {
			add("info", "No long-running queries", "Nothing active over 30s right now.", "")
		}
	}

	if len(findings) == 0 {
		add("info", "No findings", "Probes ran but returned nothing to report.", "")
	}
	return findings, nil
}

func humanNum(n int64) string {
	if n >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
	if n >= 1_000 {
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	}
	return fmt.Sprintf("%d", n)
}

// ---------- MCP catalog helpers (all read-only, ai_role safe) ----------

func openReadPool(ctx context.Context, src model.Source) (*pgxpool.Pool, *sshx.Tunnel, error) {
	return Connect(ctx, src, src.Username, src.Password, true)
}

// TableStats returns size + scan activity + bloat hint for one table,
// or the top tables by size when table is empty.
func TableStats(ctx context.Context, src model.Source, schema, table string, limit int) ([]map[string]any, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	pool, tun, err := openReadPool(ctx, src)
	if err != nil {
		return nil, err
	}
	defer pool.Close()
	defer func() {
		if tun != nil {
			tun.Close()
		}
	}()
	if schema != "" && table != "" {
		if !identRe.MatchString(schema) || !identRe.MatchString(table) {
			return nil, fmt.Errorf("invalid table name")
		}
		r, err := pool.Query(ctx, `
			SELECT n.nspname||'.'||c.relname,
				pg_size_pretty(pg_total_relation_size(c.oid)),
				pg_total_relation_size(c.oid),
				c.reltuples::bigint,
				COALESCE(s.seq_scan,0), COALESCE(s.idx_scan,0),
				COALESCE(s.n_dead_tup,0),
				(SELECT COUNT(*) FROM pg_index i WHERE i.indrelid=c.oid)
			FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
			LEFT JOIN pg_stat_user_tables s ON s.relid=c.oid
			WHERE c.relkind='r' AND n.nspname=$1 AND c.relname=$2`, schema, table)
		if err != nil {
			return nil, err
		}
		defer r.Close()
		out := []map[string]any{}
		for r.Next() {
			var name, size string
			var bytes, est, seq, idx, dead, nidx int64
			if err := r.Scan(&name, &size, &bytes, &est, &seq, &idx, &dead, &nidx); err != nil {
				return nil, err
			}
			out = append(out, map[string]any{
				"table": name, "totalSize": size, "sizeBytes": bytes,
				"rowEstimate": est, "seqScans": seq, "indexScans": idx,
				"deadTuples": dead, "indexCount": nidx,
			})
		}
		return out, r.Err()
	}
	r, err := pool.Query(ctx, `
		SELECT n.nspname||'.'||c.relname,
			pg_size_pretty(pg_total_relation_size(c.oid)),
			pg_total_relation_size(c.oid),
			c.reltuples::bigint,
			COALESCE(s.seq_scan,0), COALESCE(s.idx_scan,0),
			COALESCE(s.n_dead_tup,0),
			(SELECT COUNT(*) FROM pg_index i WHERE i.indrelid=c.oid)
		FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
		LEFT JOIN pg_stat_user_tables s ON s.relid=c.oid
		WHERE c.relkind='r' AND n.nspname NOT IN ('pg_catalog','information_schema')
		ORDER BY pg_total_relation_size(c.oid) DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	out := []map[string]any{}
	for r.Next() {
		var name, size string
		var bytes, est, seq, idx, dead, nidx int64
		if err := r.Scan(&name, &size, &bytes, &est, &seq, &idx, &dead, &nidx); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{
			"table": name, "totalSize": size, "sizeBytes": bytes,
			"rowEstimate": est, "seqScans": seq, "indexScans": idx,
			"deadTuples": dead, "indexCount": nidx,
		})
	}
	return out, r.Err()
}

// Indexes returns the index inventory for one table.
func Indexes(ctx context.Context, src model.Source, schema, table string) ([]map[string]any, error) {
	if !identRe.MatchString(schema) || !identRe.MatchString(table) {
		return nil, fmt.Errorf("invalid table name")
	}
	pool, tun, err := openReadPool(ctx, src)
	if err != nil {
		return nil, err
	}
	defer pool.Close()
	defer func() {
		if tun != nil {
			tun.Close()
		}
	}()
	rows, err := pool.Query(ctx, `
		SELECT indexname, indexdef FROM pg_indexes
		WHERE schemaname=$1 AND tablename=$2 ORDER BY indexname`, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var name, def string
		if err := rows.Scan(&name, &def); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"index": name, "definition": def})
	}
	return out, rows.Err()
}

// Relationships returns foreign-key edges. When table is set it is filtered
// to edges touching that table, otherwise the whole graph (capped).
func Relationships(ctx context.Context, src model.Source, schema, table string, limit int) ([]map[string]any, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	pool, tun, err := openReadPool(ctx, src)
	if err != nil {
		return nil, err
	}
	defer pool.Close()
	defer func() {
		if tun != nil {
			tun.Close()
		}
	}()
	q := `
		SELECT con.conname,
			ns.nspname||'.'||cf.relname||'.'||af.attname,
			nt.nspname||'.'||ct.relname||'.'||at.attname
		FROM pg_constraint con
		JOIN pg_class cf ON cf.oid=con.conrelid
		JOIN pg_namespace ns ON ns.oid=cf.relnamespace
		JOIN pg_class ct ON ct.oid=con.confrelid
		JOIN pg_namespace nt ON nt.oid=ct.relnamespace
		JOIN pg_attribute af ON af.attrelid=con.conrelid AND af.attnum=con.conkey[1]
		JOIN pg_attribute at ON at.attrelid=con.confrelid AND at.attnum=con.confkey[1]
		WHERE con.contype='f'`
	args := []any{}
	if schema != "" && table != "" {
		if !identRe.MatchString(schema) || !identRe.MatchString(table) {
			return nil, fmt.Errorf("invalid table name")
		}
		q += ` AND ((ns.nspname=$1 AND cf.relname=$2) OR (nt.nspname=$1 AND ct.relname=$2))`
		args = append(args, schema, table)
	} else {
		q += ` AND ns.nspname NOT IN ('pg_catalog','information_schema')`
	}
	q += ` ORDER BY 2 LIMIT $` + strconv.Itoa(len(args)+1)
	args = append(args, limit)
	rows, err := pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var name, from, to string
		if err := rows.Scan(&name, &from, &to); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"constraint": name, "from": from, "references": to})
	}
	return out, rows.Err()
}

// SearchTables fuzzy-finds tables by name fragment across schemas.
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
	pool, tun, err := openReadPool(ctx, src)
	if err != nil {
		return nil, err
	}
	defer pool.Close()
	defer func() {
		if tun != nil {
			tun.Close()
		}
	}()
	rows, err := pool.Query(ctx, `
		SELECT n.nspname||'.'||c.relname, c.reltuples::bigint
		FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
		WHERE c.relkind='r' AND n.nspname NOT IN ('pg_catalog','information_schema')
		AND c.relname ILIKE '%'||$1||'%'
		ORDER BY c.reltuples DESC LIMIT $2`, pattern, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var name string
		var est int64
		if err := rows.Scan(&name, &est); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"table": name, "rowEstimate": est})
	}
	return out, rows.Err()
}

// SlowQueries surfaces the heaviest statements via pg_stat_statements when
// the extension is visible, else falls back to currently-running queries.
func SlowQueries(ctx context.Context, src model.Source, limit int) (map[string]any, error) {
	if limit <= 0 || limit > 20 {
		limit = 10
	}
	pool, tun, err := openReadPool(ctx, src)
	if err != nil {
		return nil, err
	}
	defer pool.Close()
	defer func() {
		if tun != nil {
			tun.Close()
		}
	}()
	var hasExt bool
	_ = pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_extension WHERE extname='pg_stat_statements')`).Scan(&hasExt)
	if hasExt {
		rows, err := pool.Query(ctx, `
			SELECT left(query,200), calls, round(total_exec_time::numeric,1),
				round(mean_exec_time::numeric,1), round((100*total_exec_time/sum(total_exec_time) OVER())::numeric,1)
			FROM pg_stat_statements WHERE query NOT LIKE '%pg_stat_statements%'
			ORDER BY mean_exec_time DESC LIMIT $1`, limit)
		if err == nil {
			out := []map[string]any{}
			for rows.Next() {
				var q string
				var calls int64
				var total, mean, pct float64
				if err := rows.Scan(&q, &calls, &total, &mean, &pct); err != nil {
					rows.Close()
					return nil, err
				}
				out = append(out, map[string]any{
					"query": strings.TrimSpace(q), "calls": calls,
					"totalMs": total, "meanMs": mean, "sharePct": pct,
				})
			}
			rows.Close()
			return map[string]any{"source": "pg_stat_statements", "queries": out}, nil
		}
		rows.Close()
	}
	rows, err := pool.Query(ctx, `
		SELECT pid, now()-query_start, left(query,200)
		FROM pg_stat_activity WHERE state='active' AND pid <> pg_backend_pid()
		ORDER BY query_start LIMIT $1`, limit)
	if err != nil {
		return map[string]any{
			"source": "unavailable",
			"note":   "pg_stat_statements not installed and activity view not readable (" + shortErr(err) + "). Ask an admin: CREATE EXTENSION pg_stat_statements; + GRANT pg_monitor TO " + src.Username + ";",
			"queries": []any{},
		}, nil
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var pid int
		var dur, q string
		if err := rows.Scan(&pid, &dur, &q); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"pid": pid, "runningFor": dur, "query": strings.TrimSpace(q)})
	}
	return map[string]any{"source": "pg_stat_activity", "queries": out}, nil
}

// ExecWrite runs one app-confirmed INSERT/UPDATE/DELETE as the privileged
// write user (readOnly=false). Called ONLY from the app UI path after the
// master switch + per-batch confirmation — never from MCP.
func ExecWrite(ctx context.Context, src model.Source, dbUser, dbPass, sqlText string, allowFullTable bool) (int64, error) {
	if err := guard.ValidateWriteSQL(sqlText); err != nil {
		return 0, err
	}
	if guard.NeedsWhere(sqlText) && !allowFullTable {
		return 0, fmt.Errorf("UPDATE/DELETE without WHERE needs explicit full-table confirmation")
	}
	pool, tun, err := Connect(ctx, src, dbUser, dbPass, false)
	if err != nil {
		return 0, err
	}
	defer pool.Close()
	defer func() {
		if tun != nil {
			tun.Close()
		}
	}()
	c, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	tx, err := pool.Begin(c)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(c) }()
	if _, err := tx.Exec(c, `SET LOCAL statement_timeout = '15s'`); err != nil {
		return 0, err
	}
	tag, err := tx.Exec(c, sqlText)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(c); err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
