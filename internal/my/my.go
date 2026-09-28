// Package my implements the ReadGate gateway for the MySQL wire family:
// MySQL, MariaDB (same protocol) and TiDB (MySQL-compatible).
// Pure Go via go-sql-driver/mysql. Every read runs inside a READ ONLY
// transaction (best effort with direct fallback); the SQL guard rejects
// writes on top. AI access uses a dedicated SELECT-only user created by
// ProvisionRole with temporary admin credentials (never stored).
package my

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"readgate/internal/guard"
	"readgate/internal/model"
	"readgate/internal/sshx"

	"github.com/go-sql-driver/mysql"
)

var identRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_$]*$`)

// rawDeny mirrors pg's fragment filter for raw WHERE pieces.
var rawDeny = regexp.MustCompile(`(?i)(;|--|/\*|\b(INSERT|UPDATE|DELETE|MERGE|DROP|CREATE|ALTER|TRUNCATE|GRANT|REVOKE|COPY|VACUUM|CALL|DO|INTO|LOAD|EXECUTE|PERFORM|OUTFILE|DUMPFILE)\b)`)

// dial resolves host/port through an SSH tunnel when needed.
func dial(src model.Source) (host string, port int, closeTun func(), err error) {
	host, port = src.Host, src.Port
	if port == 0 {
		port = 3306
	}
	if src.Mode != model.ModeSSH {
		return host, port, func() {}, nil
	}
	remoteHost := src.Host
	if remoteHost == "" || remoteHost == "localhost" {
		remoteHost = "127.0.0.1"
	}
	keyPath, password := src.SSHKeyPath, src.SSHPassword
	if src.SSHAuth == "password" {
		keyPath = ""
	}
	if src.SSHAuth == "agent" {
		keyPath, password = "", ""
	}
	tun, err := sshx.Dial(src.SSHHost, src.SSHPort, src.SSHUser, keyPath, src.SSHKeyPassphrase, password, remoteHost, port, 12*time.Second)
	if err != nil {
		return "", 0, nil, fmt.Errorf("ssh tunnel: %w", err)
	}
	return "127.0.0.1", tun.LocalPort, func() { tun.Close() }, nil
}

func dsn(host string, port int, db, user, pass string) string {
	cfg := mysql.NewConfig()
	cfg.Net = "tcp"
	cfg.Addr = net.JoinHostPort(host, strconv.Itoa(port))
	cfg.User = user
	cfg.Passwd = pass
	cfg.DBName = db
	cfg.Params = map[string]string{"timeout": "8s", "readTimeout": "15s", "writeTimeout": "15s"}
	cfg.ParseTime = true
	return cfg.FormatDSN()
}

// open connects as user/pass to dbName ("" = no default database).
func open(ctx context.Context, src model.Source, user, pass, dbName string) (*sql.DB, func(), error) {
	host, port, closeTun, err := dial(src)
	if err != nil {
		return nil, nil, err
	}
	d := dsn(host, port, dbName, user, pass)
	db, err := sql.Open("mysql", d)
	if err != nil {
		closeTun()
		return nil, nil, err
	}
	db.SetMaxOpenConns(4)
	db.SetConnMaxIdleTime(2 * time.Minute)
	c, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := db.PingContext(c); err != nil {
		db.Close()
		closeTun()
		return nil, nil, fmt.Errorf("mysql ping: %w", err)
	}
	return db, func() { db.Close(); closeTun() }, nil
}

func openAI(ctx context.Context, src model.Source) (*sql.DB, func(), error) {
	return open(ctx, src, src.Username, src.Password, src.Database)
}

// roQuery runs q inside a READ ONLY transaction (direct fallback for
// servers that reject the flag), collects rows, and normalizes values.
func roQuery(ctx context.Context, db *sql.DB, q string, args ...any) ([]string, [][]any, error) {
	c, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	tx, err := db.BeginTx(c, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return directQuery(c, db, q, args...)
	}
	rows, err := tx.QueryContext(c, q, args...)
	if err != nil {
		_ = tx.Rollback()
		return nil, nil, err
	}
	cols, data, err := scanAll(rows)
	_ = rows.Close()
	_ = tx.Rollback()
	if err != nil {
		return nil, nil, err
	}
	return cols, data, nil
}

func directQuery(c context.Context, db *sql.DB, q string, args ...any) ([]string, [][]any, error) {
	rows, err := db.QueryContext(c, q, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	return scanAll(rows)
}

func scanAll(rows *sql.Rows) ([]string, [][]any, error) {
	cols, err := rows.Columns()
	if err != nil {
		return nil, nil, err
	}
	var data [][]any
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
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

func normVal(v any) any {
	switch t := v.(type) {
	case []byte:
		if !utf8.Valid(t) {
			return fmt.Sprintf("<binary %d bytes>", len(t))
		}
		return string(t)
	case time.Time:
		return t.Format(time.RFC3339Nano)
	default:
		return v
	}
}

func q1(ctx context.Context, db *sql.DB, dest any, q string, args ...any) error {
	c, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return db.QueryRowContext(c, q, args...).Scan(dest)
}

func backtick(s string) string {
	return "`" + strings.ReplaceAll(s, "`", "``") + "`"
}

func pushCheck(out *[]model.CheckResult, key, label string, fn func() (string, error)) {
	start := time.Now()
	detail, err := fn()
	*out = append(*out, model.CheckResult{Key: key, Label: label, OK: err == nil, Detail: detail, Duration: time.Since(start).Milliseconds()})
	if err != nil && detail == "" {
		(*out)[len(*out)-1].Detail = err.Error()
	}
}

func shortErr(err error) string {
	s := err.Error()
	if len(s) > 160 {
		return s[:160] + "…"
	}
	return s
}

// VerifyReadOnly proves the AI identity is SELECT-only:
// connect → version → database → SELECT → write denied → grants audit.
func VerifyReadOnly(ctx context.Context, src model.Source, dbUser, dbPass string) []model.CheckResult {
	db, close, err := open(ctx, src, dbUser, dbPass, src.Database)
	if err != nil {
		return []model.CheckResult{{Key: "select", Label: "Connect as AI user", Detail: err.Error()}}
	}
	defer close()
	out := []model.CheckResult{}
	pushCheck(&out, "connect", "MySQL connection successful", func() (string, error) {
		var ver string
		if err := q1(ctx, db, &ver, `SELECT VERSION()`); err != nil {
			return "", err
		}
		return shortenVer(ver), nil
	})
	pushCheck(&out, "auth", "Database accessible", func() (string, error) {
		var cur string
		if err := q1(ctx, db, &cur, `SELECT DATABASE()`); err != nil {
			return "", err
		}
		return "using " + cur, nil
	})
	pushCheck(&out, "select", "SELECT permission verified", func() (string, error) {
		var one int
		if err := q1(ctx, db, &one, `SELECT 1`); err != nil {
			return "", err
		}
		var n int
		_ = q1(ctx, db, &n, `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE()`)
		return fmt.Sprintf("SELECT 1 → 1 · %d tables visible", n), nil
	})
	pushCheck(&out, "write", "Write permission denied", func() (string, error) {
		c, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		tx, err := db.BeginTx(c, &sql.TxOptions{ReadOnly: true})
		if err != nil {
			return "read-only transactions enforced (" + shortErr(err) + ")", nil
		}
		defer tx.Rollback()
		if _, err := tx.ExecContext(c, `CREATE TABLE __rg_write_test(id INT)`); err == nil {
			_, _ = db.Exec(`DROP TABLE IF EXISTS __rg_write_test`)
			return "writes unexpectedly allowed", fmt.Errorf("AI user can create tables — refusing to enable")
		} else {
			return "rejected as read-only (" + shortErr(err) + ")", nil
		}
	})
	pushCheck(&out, "ro", "Read-only configuration verified", func() (string, error) {
		return auditGrants(ctx, db)
	})
	return out
}

var grantDeny = []string{"ALL PRIVILEGES", "INSERT", "UPDATE", "DELETE", "CREATE", "DROP", "ALTER", "GRANT OPTION", " SUPER", "RELOAD", "SHUTDOWN", "FILE"}

// auditGrants parses SHOW GRANTS: SELECT must be present, nothing
// write-ish may appear. Returns "SELECT-only …" detail on success.
func auditGrants(ctx context.Context, db *sql.DB) (string, error) {
	c, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	rows, err := db.QueryContext(c, `SHOW GRANTS`)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	hasSelect := false
	for rows.Next() {
		var g string
		if err := rows.Scan(&g); err != nil {
			return "", err
		}
		up := strings.ToUpper(g)
		for _, bad := range grantDeny {
			if strings.Contains(up, bad) {
				return g, fmt.Errorf("grant too wide: %s", g)
			}
		}
		if strings.Contains(up, "SELECT") {
			hasSelect = true
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if !hasSelect {
		return "", fmt.Errorf("no SELECT grant found")
	}
	return "SELECT-only grants · writes denied server-side", nil
}

func shortenVer(v string) string {
	if i := strings.Index(v, "-"); i > 0 {
		return v[:i]
	}
	if len(v) > 40 {
		return v[:40]
	}
	return v
}

// ProbeAdmin checks the temporary admin leg: login → CREATE USER
// privilege → target database exists.
func ProbeAdmin(ctx context.Context, src model.Source, adminUser, adminPass string) []model.CheckResult {
	out := []model.CheckResult{}
	db, close, err := open(ctx, src, adminUser, adminPass, "mysql")
	if err != nil {
		return []model.CheckResult{{Key: "adm-auth", Label: "Admin login", Detail: err.Error()}}
	}
	defer close()
	pushCheck(&out, "adm-pg", "MySQL reachable as admin", func() (string, error) {
		var ver string
		if err := q1(ctx, db, &ver, `SELECT VERSION()`); err != nil {
			return "", err
		}
		return "logged in as " + adminUser + " · " + shortenVer(ver), nil
	})
	pushCheck(&out, "adm-priv", "Admin can CREATE USER", func() (string, error) {
		c, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		rows, err := db.QueryContext(c, `SHOW GRANTS`)
		if err != nil {
			return "", err
		}
		defer rows.Close()
		for rows.Next() {
			var g string
			rows.Scan(&g)
			up := strings.ToUpper(g)
			if strings.Contains(up, "ALL PRIVILEGES") || strings.Contains(up, "CREATE USER") {
				return "can create users", nil
			}
		}
		return "", fmt.Errorf("admin lacks CREATE USER (needs ALL or CREATE USER)")
	})
	pushCheck(&out, "adm-db", "Target database exists", func() (string, error) {
		var s string
		if err := q1(ctx, db, &s, `SELECT SCHEMA_NAME FROM information_schema.schemata WHERE SCHEMA_NAME = ?`, src.Database); err != nil {
			return "", fmt.Errorf("database %q not found: %v", src.Database, err)
		}
		return "database " + s + " exists", nil
	})
	return out
}

func escLit(s string) string { return strings.ReplaceAll(s, `'`, `''`) }

// ProvisionSQL returns the manual script: create user + SELECT-only grants.
func ProvisionSQL(dbName, aiUser, aiPass string) string {
	return fmt.Sprintf(`-- ReadGate · read-only AI user for %s
CREATE USER IF NOT EXISTS '%s'@'%%' IDENTIFIED BY '%s';
ALTER USER '%s'@'%%' IDENTIFIED BY '%s';
GRANT SELECT, SHOW VIEW ON %s.* TO '%s'@'%%';
FLUSH PRIVILEGES;`,
		dbName, escLit(aiUser), escLit(aiPass), escLit(aiUser), escLit(aiPass),
		backtick(dbName), escLit(aiUser))
}

// ProvisionRole creates/resets the AI user with temp admin creds.
func ProvisionRole(ctx context.Context, src model.Source, adminUser, adminPass, aiUser, aiPass string) []model.CheckResult {
	start := time.Now()
	fail := func(detail string) []model.CheckResult {
		return []model.CheckResult{{Key: "provision", Label: "Create dedicated read-only user", Detail: detail, Duration: time.Since(start).Milliseconds()}}
	}
	db, close, err := open(ctx, src, adminUser, adminPass, "mysql")
	if err != nil {
		return fail(err.Error())
	}
	defer close()
	exec := func(q string) error {
		c, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		_, err := db.ExecContext(c, q)
		return err
	}
	if err := exec(fmt.Sprintf(`CREATE USER IF NOT EXISTS '%s'@'%%' IDENTIFIED BY '%s'`, escLit(aiUser), escLit(aiPass))); err != nil {
		return fail(err.Error())
	}
	if err := exec(fmt.Sprintf(`ALTER USER '%s'@'%%' IDENTIFIED BY '%s'`, escLit(aiUser), escLit(aiPass))); err != nil {
		return fail(err.Error())
	}
	if err := exec(fmt.Sprintf(`GRANT SELECT, SHOW VIEW ON %s.* TO '%s'@'%%'`, backtick(src.Database), escLit(aiUser))); err != nil {
		return fail(err.Error())
	}
	_ = exec(`FLUSH PRIVILEGES`)
	prov := model.CheckResult{Key: "provision", Label: "Create dedicated read-only user", OK: true,
		Detail: fmt.Sprintf("user %q ready · admin credentials discarded", aiUser), Duration: time.Since(start).Milliseconds()}
	var exists string
	if err := q1(ctx, db, &exists, `SELECT user FROM mysql.user WHERE user = ? LIMIT 1`, aiUser); err != nil {
		return []model.CheckResult{prov, {Key: "role-present", Label: "User visible in mysql.user", Detail: "confirm failed: " + err.Error(), Duration: time.Since(start).Milliseconds()}}
	}
	return []model.CheckResult{prov, {Key: "role-present", Label: "User visible in mysql.user", OK: true,
		Detail: fmt.Sprintf("%q exists · verified by admin session", aiUser), Duration: time.Since(start).Milliseconds()}}
}

// Schema lists base tables with approximate row counts.
func Schema(ctx context.Context, src model.Source) (model.SchemaInfo, error) {
	db, close, err := openAI(ctx, src)
	if err != nil {
		return model.SchemaInfo{}, err
	}
	defer close()
	c, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	rows, err := db.QueryContext(c, `
		SELECT table_name, COALESCE(table_rows, 0)
		FROM information_schema.tables
		WHERE table_schema = DATABASE() AND table_type = 'BASE TABLE'
		ORDER BY 1 LIMIT 500`)
	if err != nil {
		return model.SchemaInfo{}, err
	}
	defer rows.Close()
	info := model.SchemaInfo{SourceName: src.Name}
	for rows.Next() {
		var tb model.TableInfo
		var est sql.NullInt64
		if err := rows.Scan(&tb.Name, &est); err != nil {
			return model.SchemaInfo{}, err
		}
		tb.Schema = src.Database
		tb.RowEstimate = est.Int64
		info.Tables = append(info.Tables, tb)
	}
	return info, rows.Err()
}

// Columns returns one table's columns.
func Columns(ctx context.Context, src model.Source, schema, table string) ([]model.ColumnInfo, error) {
	if !identRe.MatchString(table) || len(table) > 64 {
		return nil, fmt.Errorf("invalid table name")
	}
	db, close, err := openAI(ctx, src)
	if err != nil {
		return nil, err
	}
	defer close()
	c, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	rows, err := db.QueryContext(c, `
		SELECT column_name, column_type, is_nullable, COALESCE(column_default, '')
		FROM information_schema.columns
		WHERE table_schema = DATABASE() AND table_name = ? ORDER BY ordinal_position`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.ColumnInfo
	for rows.Next() {
		var col model.ColumnInfo
		var nul string
		if err := rows.Scan(&col.Name, &col.Type, &nul, &col.Default); err != nil {
			return nil, err
		}
		col.Nullable = nul == "YES"
		out = append(out, col)
	}
	return out, rows.Err()
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
	db, close, err := openAI(ctx, src)
	if err != nil {
		return model.QueryResult{}, err
	}
	defer close()
	start := time.Now()
	cols, data, err := roQuery(ctx, db, sqlText)
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

// buildWhere mirrors pg's builder with backtick identifiers + ? holders.
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
		col := backtick(f.Column)
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
			conds = append(conds, join+col+" LIKE "+next("%"+escLike(v)+"%")+" ESCAPE '\\\\'")
		case "starts":
			conds = append(conds, join+col+" LIKE "+next(escLike(v)+"%")+" ESCAPE '\\\\'")
		case "ends":
			conds = append(conds, join+col+" LIKE "+next("%"+escLike(v))+" ESCAPE '\\\\'")
		case "like":
			conds = append(conds, join+col+" LIKE "+next(v))
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

// Preview fetches a UI-safe page with capped values.
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
	db, close, err := openAI(ctx, src)
	if err != nil {
		return model.QueryResult{}, err
	}
	defer close()
	where, args := buildWhere(filters, raw)
	q := fmt.Sprintf(`SELECT * FROM %s %s LIMIT %d OFFSET %d`, backtick(table), where, limit, offset)
	start := time.Now()
	cols, data, err := roQuery(ctx, db, q, args...)
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
		_ = q1(ctx, db, &total, fmt.Sprintf(`SELECT COUNT(*) FROM %s %s`, backtick(table), where), args...)
	}
	return model.QueryResult{
		Columns: cols, Rows: data, RowCount: len(data), TotalRows: total,
		DurationMs: time.Since(start).Milliseconds(), Truncated: len(data) >= limit,
	}, nil
}

// Explain returns EXPLAIN FORMAT=JSON (text fallback) for slow-query work.
func Explain(ctx context.Context, src model.Source, sqlText string) (string, error) {
	if err := guard.ValidateReadOnlySQL(sqlText); err != nil {
		return "", err
	}
	db, close, err := openAI(ctx, src)
	if err != nil {
		return "", err
	}
	defer close()
	trimmed := strings.TrimSuffix(strings.TrimSpace(sqlText), ";")
	c, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var out string
	if err := db.QueryRowContext(c, "EXPLAIN FORMAT=JSON "+trimmed).Scan(&out); err == nil {
		if len(out) > 8000 {
			out = out[:8000] + "…(truncated)"
		}
		return out, nil
	}
	rows, err := db.QueryContext(c, "EXPLAIN "+trimmed)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	var lines []string
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return "", err
		}
		parts := make([]string, len(cols))
		for i, v := range vals {
			parts[i] = fmt.Sprintf("%v", normVal(v))
		}
		lines = append(lines, strings.Join(parts, " | "))
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	out = strings.Join(lines, "\n")
	if len(out) > 8000 {
		out = out[:8000] + "…(truncated)"
	}
	return out, nil
}

// DoctorFindings runs read-only health probes with graceful degradation.
func DoctorFindings(ctx context.Context, src model.Source) ([]model.DoctorFinding, error) {
	db, close, err := openAI(ctx, src)
	if err != nil {
		return nil, err
	}
	defer close()
	var findings []model.DoctorFinding
	add := func(sev, title, detail, remedy string) {
		findings = append(findings, model.DoctorFinding{Severity: sev, Title: title, Detail: detail, Remedy: remedy, Source: src.Name})
	}
	ver := ""
	_ = q1(ctx, db, &ver, `SELECT VERSION()`)
	add("info", "MySQL reachable", shortenVer(ver), "")
	var tblCount int
	var totalRows sql.NullInt64
	if err := q1(ctx, db, &tblCount, `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE()`); err != nil {
		add("info", "Limited visibility: inventory", shortErr(err), "GRANT SELECT ON information_schema.* — usually implied by any grant.")
	} else {
		_ = q1(ctx, db, &totalRows, `SELECT SUM(table_rows) FROM information_schema.tables WHERE table_schema = DATABASE()`)
		add("info", fmt.Sprintf("Inventory: %d tables, ~%s rows", tblCount, humanNum(totalRows.Int64)),
			"Row counts are InnoDB estimates (exact needs COUNT(*)).", "")
		c, cancel := context.WithTimeout(ctx, 15*time.Second)
		rows, err := db.QueryContext(c, `SELECT table_name, table_rows FROM information_schema.tables WHERE table_schema = DATABASE() ORDER BY table_rows DESC LIMIT 5`)
		cancel()
		if err == nil {
			var names []string
			for rows.Next() {
				var nm string
				var est sql.NullInt64
				rows.Scan(&nm, &est)
				names = append(names, fmt.Sprintf("%s (~%s)", nm, humanNum(est.Int64)))
			}
			rows.Close()
			if len(names) > 0 {
				add("info", "Largest tables", strings.Join(names, " · "), "Start slow-query triage here.")
			}
		}
	}
	c, cancel := context.WithTimeout(ctx, 15*time.Second)
	prows, err := db.QueryContext(c, `
		SELECT t.table_name FROM information_schema.tables t
		LEFT JOIN information_schema.table_constraints c
			ON c.table_schema = t.table_schema AND c.table_name = t.table_name AND c.constraint_type = 'PRIMARY KEY'
		WHERE t.table_schema = DATABASE() AND t.table_type = 'BASE TABLE' AND c.constraint_name IS NULL LIMIT 10`)
	cancel()
	if err != nil {
		add("info", "Limited visibility: primary-key audit", shortErr(err), "")
	} else {
		for prows.Next() {
			var tbl string
			prows.Scan(&tbl)
			add("warn", "Table without primary key: "+tbl,
				"Replication, dedupe and AI-generated joins are riskier without a PK.",
				"Have an admin run ALTER TABLE "+tbl+" ADD PRIMARY KEY (id);")
		}
		prows.Close()
	}
	var threads, maxconns int
	if err := q1(ctx, db, &threads, `SHOW STATUS LIKE 'Threads_connected'`); err != nil {
		_ = q1(ctx, db, &threads, `SELECT COUNT(*) FROM information_schema.processlist`)
	}
	_ = q1(ctx, db, &maxconns, `SELECT @@max_connections`)
	if maxconns > 0 {
		pct := threads * 100 / maxconns
		if pct >= 80 {
			add("critical", fmt.Sprintf("Connection pressure %d/%d (%d%%)", threads, maxconns, pct),
				"App may soon hit max_connections.", "Add pooling (ProxySQL) or reduce idle connections.")
		} else {
			add("info", fmt.Sprintf("Connections healthy %d/%d", threads, maxconns), "", "")
		}
	}
	lc, lcancel := context.WithTimeout(ctx, 15*time.Second)
	lrows, err := db.QueryContext(lc, `
		SELECT id, time, LEFT(info, 120) FROM information_schema.processlist
		WHERE command NOT IN ('Sleep', 'Daemon', 'Binlog Dump', 'Connect') AND time > 30 AND info IS NOT NULL
		ORDER BY time DESC LIMIT 5`)
	lcancel()
	if err != nil {
		add("info", "Limited visibility: active query list", shortErr(err), "")
	} else {
		n := 0
		for lrows.Next() {
			var pid, dur int64
			var q sql.NullString
			lrows.Scan(&pid, &dur, &q)
			n++
			add("warn", fmt.Sprintf("Long-running query (id %d, %ds)", pid, dur), q.String, "Check EXPLAIN for missing indexes.")
		}
		lrows.Close()
		if n == 0 {
			add("info", "No long-running queries", "Nothing active over 30s right now.", "")
		}
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

// TableStats returns size + row estimates (top tables when table empty).
func TableStats(ctx context.Context, src model.Source, schema, table string, limit int) ([]map[string]any, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	db, close, err := openAI(ctx, src)
	if err != nil {
		return nil, err
	}
	defer close()
	q := `SELECT table_name, COALESCE(data_length,0)+COALESCE(index_length,0), COALESCE(table_rows,0), engine
		FROM information_schema.tables WHERE table_schema = DATABASE()`
	var args []any
	if table != "" {
		if !identRe.MatchString(table) {
			return nil, fmt.Errorf("invalid table name")
		}
		q += ` AND table_name = ?`
		args = append(args, table)
	}
	q += ` ORDER BY (data_length+index_length) DESC LIMIT ?`
	args = append(args, limit)
	c, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	rows, err := db.QueryContext(c, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var name string
		var bytes, est int64
		var eng sql.NullString
		if err := rows.Scan(&name, &bytes, &est, &eng); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{
			"table": src.Database + "." + name, "totalSize": prettyBytes(bytes),
			"sizeBytes": bytes, "rowEstimate": est, "engine": eng.String,
		})
	}
	return out, rows.Err()
}

func prettyBytes(n int64) string {
	if n >= 1<<30 {
		return fmt.Sprintf("%.1f GiB", float64(n)/(1<<30))
	}
	if n >= 1<<20 {
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	}
	if n >= 1<<10 {
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

// Indexes returns SHOW INDEX grouped per key.
func Indexes(ctx context.Context, src model.Source, schema, table string) ([]map[string]any, error) {
	if !identRe.MatchString(table) {
		return nil, fmt.Errorf("invalid table name")
	}
	db, close, err := openAI(ctx, src)
	if err != nil {
		return nil, err
	}
	defer close()
	c, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	rows, err := db.QueryContext(c, fmt.Sprintf(`SHOW INDEX FROM %s`, backtick(table)))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type key struct {
		unique bool
		cols   []string
	}
	order := []string{}
	seen := map[string]*key{}
	for rows.Next() {
		var tbl string
		var nonUnique int
		var kname, seq, col, coll, card, sub, packed, null, itype, comment, idxcomment string
		var nullable, visible string
		if err := rows.Scan(&tbl, &nonUnique, &kname, &seq, &col, &coll, &card, &sub, &packed, &null, &itype, &comment, &idxcomment, &visible); err != nil {
			// MariaDB variant has fewer columns — fall back to statistics.
			rows.Close()
			return indexesFallback(ctx, db, table)
		}
		k, ok := seen[kname]
		if !ok {
			k = &key{unique: nonUnique == 0}
			seen[kname] = k
			order = append(order, kname)
		}
		k.cols = append(k.cols, col)
		_ = seq
		_ = nullable
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var out []map[string]any
	for _, kname := range order {
		k := seen[kname]
		out = append(out, map[string]any{"index": kname, "unique": k.unique, "definition": "BTREE (" + strings.Join(k.cols, ", ") + ")"})
	}
	return out, nil
}

func indexesFallback(ctx context.Context, db *sql.DB, table string) ([]map[string]any, error) {
	c, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	rows, err := db.QueryContext(c, `
		SELECT index_name, GROUP_CONCAT(column_name ORDER BY seq_in_index), MIN(non_unique)
		FROM information_schema.statistics
		WHERE table_schema = DATABASE() AND table_name = ? GROUP BY index_name`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var name, cols string
		var nonUnique int
		if err := rows.Scan(&name, &cols, &nonUnique); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"index": name, "unique": nonUnique == 0, "definition": "(" + cols + ")"})
	}
	return out, rows.Err()
}

// Relationships returns foreign-key edges.
func Relationships(ctx context.Context, src model.Source, schema, table string, limit int) ([]map[string]any, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	db, close, err := openAI(ctx, src)
	if err != nil {
		return nil, err
	}
	defer close()
	q := `
		SELECT k.constraint_name, k.table_name, k.column_name, k.referenced_table_name, k.referenced_column_name
		FROM information_schema.key_column_usage k
		JOIN information_schema.referential_constraints r
			ON r.constraint_schema = k.constraint_schema AND r.constraint_name = k.constraint_name AND r.table_name = k.table_name
		WHERE k.table_schema = DATABASE() AND k.referenced_table_name IS NOT NULL`
	var args []any
	if table != "" {
		if !identRe.MatchString(table) {
			return nil, fmt.Errorf("invalid table name")
		}
		q += ` AND (k.table_name = ? OR k.referenced_table_name = ?)`
		args = append(args, table, table)
	}
	q += ` ORDER BY 2 LIMIT ?`
	args = append(args, limit)
	c, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	rows, err := db.QueryContext(c, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var name, from, fcol, to, tcol string
		if err := rows.Scan(&name, &from, &fcol, &to, &tcol); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{
			"constraint": name, "from": src.Database + "." + from + "." + fcol,
			"references": src.Database + "." + to + "." + tcol,
		})
	}
	return out, rows.Err()
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
	db, close, err := openAI(ctx, src)
	if err != nil {
		return nil, err
	}
	defer close()
	c, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	rows, err := db.QueryContext(c, `
		SELECT table_name, COALESCE(table_rows, 0) FROM information_schema.tables
		WHERE table_schema = DATABASE() AND table_type = 'BASE TABLE' AND table_name LIKE CONCAT('%', ?, '%')
		ORDER BY table_rows DESC LIMIT ?`, pattern, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var n string
		var est int64
		if err := rows.Scan(&n, &est); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"table": src.Database + "." + n, "rowEstimate": est})
	}
	return out, rows.Err()
}

// SlowQueries surfaces heavy statements (P_S → TiDB summary → processlist).
func SlowQueries(ctx context.Context, src model.Source, limit int) (map[string]any, error) {
	if limit <= 0 || limit > 20 {
		limit = 10
	}
	db, close, err := openAI(ctx, src)
	if err != nil {
		return nil, err
	}
	defer close()
	c, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	rows, err := db.QueryContext(c, `
		SELECT LEFT(digest_text, 200), count_star,
			ROUND(sum_timer_wait/1000000000, 1), ROUND(avg_timer_wait/1000000000, 3)
		FROM performance_schema.events_statements_summary_by_digest
		WHERE digest_text IS NOT NULL AND digest_text NOT LIKE '%events_statements_summary%'
		ORDER BY avg_timer_wait DESC LIMIT ?`, limit)
	if err == nil {
		out := []map[string]any{}
		for rows.Next() {
			var q string
			var calls int64
			var total, mean float64
			if err := rows.Scan(&q, &calls, &total, &mean); err != nil {
				rows.Close()
				return nil, err
			}
			out = append(out, map[string]any{"query": strings.TrimSpace(q), "calls": calls, "totalMs": total, "meanMs": mean})
		}
		rows.Close()
		return map[string]any{"source": "performance_schema", "queries": out}, nil
	}
	rows.Close()
	trows, err := db.QueryContext(c, `
		SELECT LEFT(query_sample_text, 200), exec_count,
			ROUND(sum_latency/1000000, 1), ROUND(avg_latency/1000000, 3)
		FROM information_schema.statements_summary
		ORDER BY avg_latency DESC LIMIT ?`, limit)
	if err == nil {
		out := []map[string]any{}
		for trows.Next() {
			var q string
			var calls int64
			var total, mean float64
			if err := trows.Scan(&q, &calls, &total, &mean); err != nil {
				trows.Close()
				return nil, err
			}
			out = append(out, map[string]any{"query": strings.TrimSpace(q), "calls": calls, "totalMs": total, "meanMs": mean})
		}
		trows.Close()
		return map[string]any{"source": "statements_summary", "queries": out}, nil
	}
	prows, err := db.QueryContext(c, `
		SELECT id, time, LEFT(info, 200) FROM information_schema.processlist
		WHERE command NOT IN ('Sleep', 'Daemon', 'Binlog Dump', 'Connect') AND info IS NOT NULL
		ORDER BY time DESC LIMIT ?`, limit)
	if err != nil {
		return map[string]any{"source": "unavailable", "note": "statement stats not readable (" + shortErr(err) + ")",
			"queries": []any{}}, nil
	}
	defer prows.Close()
	out := []map[string]any{}
	for prows.Next() {
		var pid, secs int64
		var q sql.NullString
		if err := prows.Scan(&pid, &secs, &q); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"pid": pid, "runningForSec": secs, "query": strings.TrimSpace(q.String)})
	}
	return map[string]any{"source": "processlist", "queries": out}, nil
}

// Diagnose runs server-side health over plain SQL (no SSH needed).
func Diagnose(ctx context.Context, src model.Source) []model.CheckResult {
	db, close, err := open(ctx, src, src.Username, src.Password, src.Database)
	if err != nil {
		return []model.CheckResult{{Key: "sql-reachable", Label: "MySQL reachable", Detail: err.Error()}}
	}
	defer close()
	out := []model.CheckResult{}
	pushCheck(&out, "sql-reachable", "MySQL reachable", func() (string, error) {
		var ver string
		if err := q1(ctx, db, &ver, `SELECT VERSION()`); err != nil {
			return "", err
		}
		return shortenVer(ver), nil
	})
	pushCheck(&out, "threads", "Connection load", func() (string, error) {
		var threads, maxconns int
		_ = q1(ctx, db, &threads, `SELECT COUNT(*) FROM information_schema.processlist`)
		_ = q1(ctx, db, &maxconns, `SELECT @@max_connections`)
		return fmt.Sprintf("%d threads / %d max", threads, maxconns), nil
	})
	return out
}

// ExecWrite runs one app-confirmed INSERT/UPDATE/DELETE as the privileged
// write user. Called ONLY from the app UI path — never from MCP.
func ExecWrite(ctx context.Context, src model.Source, dbUser, dbPass, sqlText string, allowFullTable bool) (int64, error) {
	if err := guard.ValidateWriteSQL(sqlText); err != nil {
		return 0, err
	}
	if guard.NeedsWhere(sqlText) && !allowFullTable {
		return 0, fmt.Errorf("UPDATE/DELETE without WHERE needs explicit full-table confirmation")
	}
	db, close, err := open(ctx, src, dbUser, dbPass, src.Database)
	if err != nil {
		return 0, err
	}
	defer close()
	c, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	tx, err := db.BeginTx(c, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(c, sqlText)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
