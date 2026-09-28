// Package mssql implements the ReadGate gateway for Microsoft SQL Server
// over TDS (pure Go via go-mssqldb). T-SQL notes: TOP (n) instead of LIMIT,
// OFFSET xx ROWS FETCH NEXT yy ROWS ONLY for paging, [bracket] identifiers,
// plans via SET SHOWPLAN_ALL. Reads run plain (the login is db_datareader);
// the SQL guard plus an EXEC/EXECUTE ban rejects writes on top. AI access
// uses a dedicated datareader user created by ProvisionRole with temporary
// admin credentials (never stored).
package mssql

import (
	"context"
	"database/sql"
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

	_ "github.com/microsoft/go-mssqldb"
)

var identRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_@$#]*$`)

// execDeny bans procedure execution: EXEC can run xp_cmdshell-class
// writers, so T-SQL reads stay strictly declarative.
var execDeny = regexp.MustCompile(`(?i)\b(EXEC(UTE)?)\b`)

// rawDeny mirrors pg's fragment filter for raw WHERE pieces.
var rawDeny = regexp.MustCompile(`(?i)(;|--|/\*|\b(INSERT|UPDATE|DELETE|MERGE|DROP|CREATE|ALTER|TRUNCATE|GRANT|REVOKE|COPY|VACUUM|CALL|DO|INTO|LOAD|EXECUTE|PERFORM|EXEC)\b)`)

func dial(src model.Source) (host string, port int, closeTun func(), err error) {
	host, port = src.Host, src.Port
	if port == 0 {
		port = 1433
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
	u := &url.URL{Scheme: "sqlserver", User: url.UserPassword(user, pass), Host: net.JoinHostPort(host, strconv.Itoa(port))}
	q := u.Query()
	if db != "" {
		q.Set("database", db)
	}
	q.Set("connection timeout", "8")
	q.Set("dial timeout", "8")
	q.Set("encryption", "disable")
	u.RawQuery = q.Encode()
	return u.String()
}

// open connects as user/pass to dbName ("" = server default).
func open(ctx context.Context, src model.Source, user, pass, dbName string) (*sql.DB, func(), error) {
	host, port, closeTun, err := dial(src)
	if err != nil {
		return nil, nil, err
	}
	db, err := sql.Open("sqlserver", dsn(host, port, dbName, user, pass))
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
		return nil, nil, fmt.Errorf("sqlserver ping: %w", err)
	}
	return db, func() { db.Close(); closeTun() }, nil
}

func openAI(ctx context.Context, src model.Source) (*sql.DB, func(), error) {
	return open(ctx, src, src.Username, src.Password, src.Database)
}

func bracket(s string) string {
	return "[" + strings.ReplaceAll(s, "]", "]]") + "]"
}

func q1(ctx context.Context, db *sql.DB, dest any, q string, args ...any) error {
	c, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return db.QueryRowContext(c, q, args...).Scan(dest)
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

func checkSQL(sqlText string) error {
	if err := guard.ValidateReadOnlySQL(sqlText); err != nil {
		return err
	}
	upper := strings.ToUpper(sqlText)
	_ = upper
	if execDeny.MatchString(stripLiterals(sqlText)) {
		return fmt.Errorf("blocked keyword %q — gateway is read-only", "EXEC")
	}
	return nil
}

var litStrip = regexp.MustCompile(`'(?:[^']|'')*'|"[^"]*"|\[[^\]]*\]`)

func stripLiterals(s string) string {
	return litStrip.ReplaceAllString(s, "''")
}

// VerifyReadOnly proves the AI login is datareader-only:
// connect → version → database → SELECT → write denied → role proof.
func VerifyReadOnly(ctx context.Context, src model.Source, dbUser, dbPass string) []model.CheckResult {
	db, close, err := open(ctx, src, dbUser, dbPass, src.Database)
	if err != nil {
		return []model.CheckResult{{Key: "select", Label: "Connect as AI user", Detail: err.Error()}}
	}
	defer close()
	out := []model.CheckResult{}
	pushCheck(&out, "connect", "SQL Server connection successful", func() (string, error) {
		var ver string
		if err := q1(ctx, db, &ver, `SELECT @@VERSION`); err != nil {
			return "", err
		}
		if i := strings.Index(ver, "\n"); i > 0 {
			ver = ver[:i]
		}
		if len(ver) > 60 {
			ver = ver[:60]
		}
		return ver, nil
	})
	pushCheck(&out, "auth", "Database accessible", func() (string, error) {
		var cur string
		if err := q1(ctx, db, &cur, `SELECT DB_NAME()`); err != nil {
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
		_ = q1(ctx, db, &n, `SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_TYPE = 'BASE TABLE'`)
		return fmt.Sprintf("SELECT 1 → 1 · %d tables visible", n), nil
	})
	pushCheck(&out, "write", "Write permission denied", func() (string, error) {
		c, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if _, err := db.ExecContext(c, `CREATE TABLE __rg_write_test(id INT)`); err == nil {
			_, _ = db.Exec(`DROP TABLE __rg_write_test`)
			return "writes unexpectedly allowed", fmt.Errorf("AI login can create tables — refusing to enable")
		} else {
			return "rejected as read-only (" + shortErr(err) + ")", nil
		}
	})
	pushCheck(&out, "ro", "Read-only configuration verified", func() (string, error) {
		var reader, writer, admin int
		_ = q1(ctx, db, &reader, `SELECT IS_ROLEMEMBER('db_datareader')`)
		_ = q1(ctx, db, &writer, `SELECT IS_ROLEMEMBER('db_datawriter')`)
		_ = q1(ctx, db, &admin, `SELECT IS_SRVROLEMEMBER('sysadmin')`)
		if reader != 1 {
			return "", fmt.Errorf("login is not db_datareader")
		}
		if writer == 1 || admin == 1 {
			return "", fmt.Errorf("login has write/admin role (datawriter/sysadmin)")
		}
		return "db_datareader only · no datawriter · no sysadmin", nil
	})
	return out
}

// ProbeAdmin checks the temporary admin leg: login → server privilege →
// target database exists.
func ProbeAdmin(ctx context.Context, src model.Source, adminUser, adminPass string) []model.CheckResult {
	out := []model.CheckResult{}
	db, close, err := open(ctx, src, adminUser, adminPass, "master")
	if err != nil {
		return []model.CheckResult{{Key: "adm-auth", Label: "Admin login", Detail: err.Error()}}
	}
	defer close()
	pushCheck(&out, "adm-pg", "SQL Server reachable as admin", func() (string, error) {
		var one int
		if err := q1(ctx, db, &one, `SELECT 1`); err != nil {
			return "", err
		}
		return "logged in as " + adminUser, nil
	})
	pushCheck(&out, "adm-priv", "Admin can create logins", func() (string, error) {
		var ok int
		if err := q1(ctx, db, &ok, `SELECT CASE WHEN IS_SRVROLEMEMBER('sysadmin') = 1 OR HAS_PERMS_BY_NAME(NULL, NULL, 'ALTER ANY LOGIN') = 1 THEN 1 ELSE 0 END`); err != nil {
			return "", err
		}
		if ok != 1 {
			return "", fmt.Errorf("admin cannot ALTER ANY LOGIN (needs sysadmin or the permission)")
		}
		return "can create logins", nil
	})
	pushCheck(&out, "adm-db", "Target database exists", func() (string, error) {
		var id sql.NullInt64
		if err := q1(ctx, db, &id, `SELECT DB_ID(@p1)`, src.Database); err != nil || !id.Valid {
			return "", fmt.Errorf("database %q not found", src.Database)
		}
		return "database " + src.Database + " exists", nil
	})
	return out
}

func escPass(s string) string { return strings.ReplaceAll(s, `'`, `''`) }

// ProvisionSQL returns the manual script: login + user + datareader.
func ProvisionSQL(dbName, aiUser, aiPass string) string {
	lu, lb := bracket(aiUser), bracket(dbName)
	return fmt.Sprintf(`-- ReadGate · read-only AI login for %s (run as admin on master)
CREATE LOGIN %s WITH PASSWORD = '%s';
USE %s;
CREATE USER %s FOR LOGIN %s;
ALTER ROLE db_datareader ADD MEMBER %s;`,
		dbName, lu, escPass(aiPass), lb, lu, lu, lu)
}

// ProvisionRole creates the datareader login with temp admin creds.
func ProvisionRole(ctx context.Context, src model.Source, adminUser, adminPass, aiUser, aiPass string) []model.CheckResult {
	start := time.Now()
	fail := func(detail string) []model.CheckResult {
		return []model.CheckResult{{Key: "provision", Label: "Create dedicated read-only login", Detail: detail, Duration: time.Since(start).Milliseconds()}}
	}
	db, close, err := open(ctx, src, adminUser, adminPass, "master")
	if err != nil {
		return fail(err.Error())
	}
	defer close()
	exec := func(q string, args ...any) error {
		c, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		_, err := db.ExecContext(c, q, args...)
		return err
	}
	var exists int
	_ = q1(ctx, db, &exists, `SELECT COUNT(*) FROM sys.server_principals WHERE name = @p1`, aiUser)
	if exists == 0 {
		if err := exec(fmt.Sprintf(`CREATE LOGIN %s WITH PASSWORD = '%s'`, bracket(aiUser), escPass(aiPass))); err != nil {
			return fail(err.Error())
		}
	}
	useQ := fmt.Sprintf(`USE %s`, bracket(src.Database))
	if err := exec(useQ); err != nil {
		return fail(err.Error())
	}
	var uexists int
	_ = q1(ctx, db, &uexists, `SELECT COUNT(*) FROM sys.database_principals WHERE name = @p1`, aiUser)
	if uexists == 0 {
		if err := exec(fmt.Sprintf(`CREATE USER %s FOR LOGIN %s`, bracket(aiUser), bracket(aiUser))); err != nil {
			return fail(err.Error())
		}
	}
	if err := exec(fmt.Sprintf(`ALTER ROLE db_datareader ADD MEMBER %s`, bracket(aiUser))); err != nil {
		if !strings.Contains(err.Error(), "already a member") {
			return fail(err.Error())
		}
	}
	prov := model.CheckResult{Key: "provision", Label: "Create dedicated read-only login", OK: true,
		Detail: fmt.Sprintf("login %q is db_datareader · admin credentials discarded", aiUser), Duration: time.Since(start).Milliseconds()}
	var chk int
	if err := q1(ctx, db, &chk, `SELECT COUNT(*) FROM sys.server_principals WHERE name = @p1`, aiUser); err != nil || chk == 0 {
		return []model.CheckResult{prov, {Key: "role-present", Label: "Login visible in sys.server_principals", Detail: "confirm failed", Duration: time.Since(start).Milliseconds()}}
	}
	return []model.CheckResult{prov, {Key: "role-present", Label: "Login visible in sys.server_principals", OK: true,
		Detail: fmt.Sprintf("%q exists · verified by admin session", aiUser), Duration: time.Since(start).Milliseconds()}}
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

func q1row(ctx context.Context, db *sql.DB, dest any, q string, args ...any) error {
	c, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return db.QueryRowContext(c, q, args...).Scan(dest)
}

// top injects TOP (n) into a leading SELECT (WITH queries pass through and
// rely on the client-side cap + timeout instead).
func top(sqlText string, n int) string {
	upper := strings.ToUpper(strings.TrimSpace(sqlText))
	if !strings.HasPrefix(upper, "SELECT") {
		return sqlText
	}
	re := regexp.MustCompile(`(?i)^\s*SELECT(\s+DISTINCT)?`)
	if loc := re.FindStringIndex(sqlText); loc != nil {
		m := re.FindString(sqlText)
		return sqlText[:loc[0]] + strings.TrimSuffix(m, " ") + fmt.Sprintf(" TOP (%d)", n) + sqlText[loc[1]:]
	}
	return sqlText
}

// Query runs a guarded read-only statement with row cap + timeout.
func Query(ctx context.Context, src model.Source, sqlText string, limit int) (model.QueryResult, error) {
	if err := checkSQL(sqlText); err != nil {
		return model.QueryResult{}, err
	}
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	sqlText = top(sqlText, limit)
	db, close, err := openAI(ctx, src)
	if err != nil {
		return model.QueryResult{}, err
	}
	defer close()
	c, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	start := time.Now()
	rows, err := db.QueryContext(c, sqlText)
	if err != nil {
		return model.QueryResult{}, err
	}
	defer rows.Close()
	cols, data, err := scanAll(rows)
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
	s = strings.ReplaceAll(s, `[`, `[[]`)
	s = strings.ReplaceAll(s, `%`, `[%]`)
	s = strings.ReplaceAll(s, `_`, `[_]`)
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
		return 1
	case "false":
		return 0
	}
	return v
}

// buildWhere mirrors pg's builder with bracket identifiers + @pN holders.
func buildWhere(filters []model.Filter, raw string) (string, []any) {
	var conds []string
	var args []any
	next := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("@p%d", len(args))
	}
	for i, f := range filters {
		if !identRe.MatchString(f.Column) || len(f.Column) > 64 {
			continue
		}
		col := bracket(f.Column)
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
			conds = append(conds, join+col+" <> "+next(coerce(v)))
		case "gt":
			conds = append(conds, join+col+" > "+next(coerce(v)))
		case "gte":
			conds = append(conds, join+col+" >= "+next(coerce(v)))
		case "lt":
			conds = append(conds, join+col+" < "+next(coerce(v)))
		case "lte":
			conds = append(conds, join+col+" <= "+next(coerce(v)))
		case "contains":
			conds = append(conds, join+col+" LIKE "+next("%"+escLike(v)+"%"))
		case "starts":
			conds = append(conds, join+col+" LIKE "+next(escLike(v)+"%"))
		case "ends":
			conds = append(conds, join+col+" LIKE "+next("%"+escLike(v)))
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

// Schema lists base tables with best-effort row counts.
func Schema(ctx context.Context, src model.Source) (model.SchemaInfo, error) {
	db, close, err := openAI(ctx, src)
	if err != nil {
		return model.SchemaInfo{}, err
	}
	defer close()
	c, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	rows, err := db.QueryContext(c, `
		SELECT s.name, t.name FROM sys.tables t
		JOIN sys.schemas s ON s.schema_id = t.schema_id ORDER BY 1, 2`)
	if err != nil {
		return model.SchemaInfo{}, err
	}
	defer rows.Close()
	info := model.SchemaInfo{SourceName: src.Name}
	type tn struct{ schema, name string }
	var list []tn
	for rows.Next() {
		var s, t string
		if err := rows.Scan(&s, &t); err != nil {
			return model.SchemaInfo{}, err
		}
		list = append(list, tn{s, t})
		if len(list) >= 500 {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return model.SchemaInfo{}, err
	}
	for i, t := range list {
		var est int64
		if i < 100 {
			_ = q1row(ctx, db, &est, `SELECT SUM(p.rows) FROM sys.partitions p JOIN sys.tables tt ON tt.object_id = p.object_id JOIN sys.schemas ss ON ss.schema_id = tt.schema_id WHERE p.index_id IN (0, 1) AND ss.name = @p1 AND tt.name = @p2`, t.schema, t.name)
		}
		info.Tables = append(info.Tables, model.TableInfo{Schema: t.schema, Name: t.name, RowEstimate: est})
	}
	return info, nil
}

// Columns returns one table's columns.
func Columns(ctx context.Context, src model.Source, schema, table string) ([]model.ColumnInfo, error) {
	if !identRe.MatchString(table) || len(table) > 64 {
		return nil, fmt.Errorf("invalid table name")
	}
	if schema == "" || schema == "main" {
		schema = "dbo"
	}
	db, close, err := openAI(ctx, src)
	if err != nil {
		return nil, err
	}
	defer close()
	c, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	rows, err := db.QueryContext(c, `
		SELECT column_name, data_type, is_nullable, ISNULL(column_default, '')
		FROM INFORMATION_SCHEMA.COLUMNS
		WHERE table_schema = @p1 AND table_name = @p2 ORDER BY ordinal_position`, schema, table)
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

// Preview fetches a UI-safe page (OFFSET/FETCH needs ORDER BY — always set).
func Preview(ctx context.Context, src model.Source, schema, table string, limit, offset int, filters []model.Filter, raw string) (model.QueryResult, error) {
	if !identRe.MatchString(table) || len(table) > 64 {
		return model.QueryResult{}, fmt.Errorf("invalid table name")
	}
	if schema == "" || schema == "main" {
		schema = "dbo"
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
	c, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	where, args := buildWhere(filters, raw)
	off := fmt.Sprintf(" OFFSET %d ROWS FETCH NEXT %d ROWS ONLY", offset, limit)
	q := fmt.Sprintf(`SELECT * FROM %s.%s %s ORDER BY 1%s`, bracket(schema), bracket(table), where, off)
	start := time.Now()
	rows, err := db.QueryContext(c, q, args...)
	if err != nil {
		return model.QueryResult{}, err
	}
	defer rows.Close()
	cols, data, err := scanAll(rows)
	if err != nil {
		return model.QueryResult{}, err
	}
	for i, r := range data {
		for j, v := range r {
			if s, ok := v.(string); ok {
				if rr := []rune(s); len(rr) > 1000 {
					data[i][j] = string(rr[:1000]) + fmt.Sprintf("… (+%d chars)", len(rr)-1000)
				}
			}
		}
	}
	var total int64 = -1
	if where != "" {
		_ = q1row(ctx, db, &total, fmt.Sprintf(`SELECT COUNT(*) FROM %s.%s %s`, bracket(schema), bracket(table), where), args...)
	}
	return model.QueryResult{
		Columns: cols, Rows: data, RowCount: len(data), TotalRows: total,
		DurationMs: time.Since(start).Milliseconds(), Truncated: len(data) >= limit,
	}, nil
}

// Explain returns SHOWPLAN_ALL rows for slow-query reasoning.
func Explain(ctx context.Context, src model.Source, sqlText string) (string, error) {
	if err := checkSQL(sqlText); err != nil {
		return "", err
	}
	db, close, err := openAI(ctx, src)
	if err != nil {
		return "", err
	}
	defer close()
	c, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	conn, err := db.Conn(c)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(c, `SET SHOWPLAN_ALL ON`); err != nil {
		return "", err
	}
	defer conn.ExecContext(context.WithoutCancel(c), `SET SHOWPLAN_ALL OFF`)
	rows, err := conn.QueryContext(c, strings.TrimSuffix(strings.TrimSpace(sqlText), ";"))
	if err != nil {
		return "", err
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	si := 0
	for i, n := range cols {
		if strings.EqualFold(n, "StmtText") {
			si = i
		}
	}
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
		lines = append(lines, fmt.Sprintf("%v", normVal(vals[si])))
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if len(lines) == 0 {
		return "(no plan rows — statement may not be plannable)", nil
	}
	out := strings.Join(lines, "\n")
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
	if err := q1row(ctx, db, &ver, `SELECT @@VERSION`); err != nil {
		return nil, err
	}
	if i := strings.Index(ver, "\n"); i > 0 {
		ver = ver[:i]
	}
	add("info", "SQL Server reachable", ver, "")
	var tblCount int
	if err := q1row(ctx, db, &tblCount, `SELECT COUNT(*) FROM sys.tables`); err != nil {
		add("info", "Limited visibility: inventory", shortErr(err), "")
	} else {
		add("info", fmt.Sprintf("Inventory: %d tables", tblCount), "Counts per table load on browse.", "")
		c, cancel := context.WithTimeout(ctx, 20*time.Second)
		rows, err := db.QueryContext(c, `
			SELECT TOP 5 s.name + '.' + t.name, SUM(p.rows)
			FROM sys.tables t JOIN sys.schemas s ON s.schema_id = t.schema_id
			JOIN sys.partitions p ON p.object_id = t.object_id AND p.index_id IN (0, 1)
			GROUP BY s.name, t.name ORDER BY SUM(p.rows) DESC`)
		cancel()
		if err == nil {
			var names []string
			for rows.Next() {
				var nm string
				var est int64
				rows.Scan(&nm, &est)
				names = append(names, fmt.Sprintf("%s (~%d)", nm, est))
			}
			rows.Close()
			if len(names) > 0 {
				add("info", "Largest tables", strings.Join(names, " · "), "Start slow-query triage here.")
			}
		} else {
			add("info", "Limited visibility: table sizes", shortErr(err), "GRANT VIEW DATABASE STATE to the AI login for size stats.")
		}
	}
	pc, pcancel := context.WithTimeout(ctx, 15*time.Second)
	prows, err := db.QueryContext(pc, `
		SELECT TOP 10 s.name + '.' + t.name FROM sys.tables t
		JOIN sys.schemas s ON s.schema_id = t.schema_id
		WHERE NOT EXISTS (SELECT 1 FROM sys.key_constraints k WHERE k.parent_object_id = t.object_id AND k.type = 'PK')`)
	pcancel()
	if err != nil {
		add("info", "Limited visibility: primary-key audit", shortErr(err), "")
	} else {
		for prows.Next() {
			var tbl string
			prows.Scan(&tbl)
			add("warn", "Table without primary key: "+tbl,
				"Replication, dedupe and AI-generated joins are riskier without a PK.",
				"Have an admin add a PRIMARY KEY.")
		}
		prows.Close()
	}
	var conns int
	if err := q1row(ctx, db, &conns, `SELECT COUNT(*) FROM sys.dm_exec_sessions WHERE is_user_process = 1`); err != nil {
		add("info", "Limited visibility: session stats", shortErr(err), "GRANT VIEW SERVER STATE to the AI login for pressure stats.")
	} else {
		add("info", fmt.Sprintf("User sessions: %d", conns), "", "")
	}
	lc, lcancel := context.WithTimeout(ctx, 15*time.Second)
	lrows, err := db.QueryContext(lc, `
		SELECT TOP 5 r.session_id, DATEDIFF(SECOND, r.start_time, GETDATE()), LEFT(t.text, 120)
		FROM sys.dm_exec_requests r CROSS APPLY sys.dm_exec_sql_text(r.sql_handle) t
		WHERE r.status = 'running' AND DATEDIFF(SECOND, r.start_time, GETDATE()) > 30`)
	lcancel()
	if err != nil {
		add("info", "Limited visibility: active requests", shortErr(err), "")
	} else {
		n := 0
		for lrows.Next() {
			var sid, secs int
			var q sql.NullString
			lrows.Scan(&sid, &secs, &q)
			n++
			add("warn", fmt.Sprintf("Long-running request (session %d, %ds)", sid, secs), q.String, "Check the plan for missing indexes.")
		}
		lrows.Close()
		if n == 0 {
			add("info", "No long-running queries", "Nothing running over 30s right now.", "")
		}
	}
	return findings, nil
}

// TableStats returns row counts + sizes (best effort without STATE view).
func TableStats(ctx context.Context, src model.Source, schema, table string, limit int) ([]map[string]any, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	db, close, err := openAI(ctx, src)
	if err != nil {
		return nil, err
	}
	defer close()
	q := `
		SELECT s.name, t.name, SUM(p.rows),
			SUM(a.total_pages) * 8
		FROM sys.tables t
		JOIN sys.schemas s ON s.schema_id = t.schema_id
		LEFT JOIN sys.partitions p ON p.object_id = t.object_id AND p.index_id IN (0, 1)
		LEFT JOIN sys.allocation_units a ON a.container_id = p.hobt_id
		WHERE 1 = 1`
	var args []any
	if table != "" {
		if !identRe.MatchString(table) {
			return nil, fmt.Errorf("invalid table name")
		}
		q += ` AND t.name = @p1`
		args = append(args, table)
	}
	q += ` GROUP BY s.name, t.name ORDER BY SUM(p.rows) DESC`
	c, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	rows, err := db.QueryContext(c, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var s, t string
		var est, kb sql.NullInt64
		if err := rows.Scan(&s, &t, &est, &kb); err != nil {
			return nil, err
		}
		bytes := kb.Int64 * 1024
		out = append(out, map[string]any{
			"table": s + "." + t, "totalSize": prettyBytes(bytes),
			"sizeBytes": bytes, "rowEstimate": est.Int64,
		})
		if len(out) >= limit {
			break
		}
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

// Indexes returns index inventory for one table.
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
	rows, err := db.QueryContext(c, `
		SELECT i.name, i.is_unique, i.type_desc,
			(SELECT STRING_AGG(c.name, ', ') WITHIN GROUP (ORDER BY ic.key_ordinal)
			 FROM sys.index_columns ic JOIN sys.columns c ON c.object_id = ic.object_id AND c.column_id = ic.column_id
			 WHERE ic.object_id = i.object_id AND ic.index_id = i.index_id)
		FROM sys.indexes i JOIN sys.tables t ON t.object_id = i.object_id
		WHERE t.name = @p1 AND i.type > 0 ORDER BY i.name`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var name, typ string
		var unique bool
		var cols sql.NullString
		if err := rows.Scan(&name, &unique, &typ, &cols); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"index": name, "unique": unique, "definition": typ + " (" + cols.String + ")"})
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
		SELECT TOP (@p1) fk.name, fs.name + '.' + ft.name + '.' + fc.name, rs.name + '.' + rt.name + '.' + rc.name
		FROM sys.foreign_keys fk
		JOIN sys.tables ft ON ft.object_id = fk.parent_object_id
		JOIN sys.schemas fs ON fs.schema_id = ft.schema_id
		JOIN sys.tables rt ON rt.object_id = fk.referenced_object_id
		JOIN sys.schemas rs ON rs.schema_id = rt.schema_id
		JOIN sys.foreign_key_columns fkc ON fkc.constraint_object_id = fk.object_id AND fkc.constraint_column_id = 1
		JOIN sys.columns fc ON fc.object_id = fkc.parent_object_id AND fc.column_id = fkc.parent_column_id
		JOIN sys.columns rc ON rc.object_id = fkc.referenced_object_id AND rc.column_id = fkc.referenced_column_id`
	var args []any
	args = append(args, limit)
	if table != "" {
		if !identRe.MatchString(table) {
			return nil, fmt.Errorf("invalid table name")
		}
		q += ` WHERE (ft.name = @p2 OR rt.name = @p2)`
		args = append(args, table)
	}
	c, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	rows, err := db.QueryContext(c, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var name, from, to string
		if err := rows.Scan(&name, &from, &to); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"constraint": name, "from": from, "references": to})
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
		SELECT TOP (@p2) s.name + '.' + t.name FROM sys.tables t
		JOIN sys.schemas s ON s.schema_id = t.schema_id
		WHERE t.name LIKE '%' + @p1 + '%' ORDER BY t.name`, escLikeWild(pattern), limit)
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
		out = append(out, map[string]any{"table": n})
	}
	return out, rows.Err()
}

func escLikeWild(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, `[`, `[[]`), `%`, `[%]`)
}

// SlowQueries surfaces heavy cached plans (graceful without STATE view).
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
		SELECT TOP (@p1) LEFT(t.text, 200), s.execution_count,
			s.total_elapsed_time / 1000.0, s.total_elapsed_time / 1000.0 / NULLIF(s.execution_count, 0)
		FROM sys.dm_exec_query_stats s CROSS APPLY sys.dm_exec_sql_text(s.sql_handle) t
		ORDER BY s.total_elapsed_time / NULLIF(s.execution_count, 0) DESC`, limit)
	if err != nil {
		return map[string]any{"source": "unavailable",
			"note": "query stats need VIEW SERVER STATE (" + shortErr(err) + ") — ask an admin to grant it to the AI login.",
			"queries": []any{}}, nil
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var q string
		var calls int64
		var total, mean float64
		if err := rows.Scan(&q, &calls, &total, &mean); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"query": strings.TrimSpace(q), "calls": calls, "totalMs": total, "meanMs": mean})
	}
	return map[string]any{"source": "dm_exec_query_stats", "queries": out}, nil
}

// Diagnose runs server-side health over plain SQL (no SSH needed).
func Diagnose(ctx context.Context, src model.Source) []model.CheckResult {
	db, close, err := open(ctx, src, src.Username, src.Password, src.Database)
	if err != nil {
		return []model.CheckResult{{Key: "sql-reachable", Label: "SQL Server reachable", Detail: err.Error()}}
	}
	defer close()
	out := []model.CheckResult{}
	pushCheck(&out, "sql-reachable", "SQL Server reachable", func() (string, error) {
		var ver string
		if err := q1row(ctx, db, &ver, `SELECT @@VERSION`); err != nil {
			return "", err
		}
		if i := strings.Index(ver, "\n"); i > 0 {
			ver = ver[:i]
		}
		return ver, nil
	})
	pushCheck(&out, "sessions", "User sessions", func() (string, error) {
		var n int
		if err := q1row(ctx, db, &n, `SELECT COUNT(*) FROM sys.dm_exec_sessions WHERE is_user_process = 1`); err != nil {
			return "not visible (" + shortErr(err) + ")", nil
		}
		return fmt.Sprintf("%d user sessions", n), nil
	})
	return out
}

// ExecWrite runs one app-confirmed INSERT/UPDATE/DELETE as the privileged
// write login. Called ONLY from the app UI path — never from MCP.
func ExecWrite(ctx context.Context, src model.Source, dbUser, dbPass, sqlText string, allowFullTable bool) (int64, error) {
	if err := guard.ValidateWriteSQL(sqlText); err != nil {
		return 0, err
	}
	if execDeny.MatchString(stripLiterals(sqlText)) {
		return 0, fmt.Errorf("blocked keyword %q — app writes are row-DML only", "EXEC")
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
	res, err := db.ExecContext(c, sqlText)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
