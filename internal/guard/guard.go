package guard

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	multiStatement = regexp.MustCompile(`;\s*\S`)
	commentStrip   = regexp.MustCompile(`(?m)(--[^\n]*|/\*.*?\*/)`)
	// quoted text is data, not keywords: WHERE name='Grant' is a plain SELECT.
	stringLitStrip = regexp.MustCompile(`'(?:[^']|'')*'|"[^"]*"`)
	// Word-boundary matching: "created_at" must NOT trip "CREATE",
	// "updated_at" must NOT trip "UPDATE", "cluster_id" must NOT trip "CLUSTER".
	deniedRe = regexp.MustCompile(`(?i)\b(INSERT|UPDATE|DELETE|MERGE|UPSERT|DROP|CREATE|ALTER|TRUNCATE|GRANT|REVOKE|COPY|VACUUM|ANALYZE|CLUSTER|REINDEX|CALL|DO|LISTEN|NOTIFY|LOAD|RESET|BEGIN|COMMIT|ROLLBACK|SAVEPOINT|SECURITY\s+DEFINER|PG_TERMINATE|PG_CANCEL|DBLINK|INTO\s+OUTFILE|INTO\s+DUMPFILE|START\s+TRANSACTION)\b`)
	setRe    = regexp.MustCompile(`(?i)\bSET\b`)
	writeCteRe = regexp.MustCompile(`(?i)\b(INSERT|UPDATE|DELETE|MERGE)\b`)
	// EXPLAIN (ANALYZE, BUFFERS, ...) — options are planner flags, not statements.
	explainOpts = regexp.MustCompile(`(?i)^\s*EXPLAIN\s*\([^)]*\)`)
)

// ValidateReadOnlySQL enforces the gateway invariant:
// only single-statement read queries may reach the database.
func ValidateReadOnlySQL(raw string) error {
	sql := strings.TrimSpace(raw)
	if sql == "" {
		return fmt.Errorf("empty query")
	}
	// strip comments AND quoted literals for analysis
	noComments := commentStrip.ReplaceAllString(sql, " ")
	upper := strings.ToUpper(strings.TrimSpace(noComments))
	analysis := stringLitStrip.ReplaceAllString(noComments, "''")
	// EXPLAIN options are planner flags — scan the statement, not the flags.
	analysis = explainOpts.ReplaceAllString(analysis, "EXPLAIN")

	allowedPrefix := false
	for _, p := range []string{"SELECT", "WITH", "EXPLAIN", "SHOW", "DESCRIBE", "DESC ", "TABLE ", "VALUES ("} {
		if strings.HasPrefix(upper, p) {
			allowedPrefix = true
			break
		}
	}
	if !allowedPrefix {
		return fmt.Errorf("only SELECT / WITH … SELECT / EXPLAIN / SHOW queries are allowed through the gateway")
	}
	if m := deniedRe.FindString(analysis); m != "" {
		return fmt.Errorf("blocked keyword %q — gateway is read-only", strings.ToUpper(strings.Join(strings.Fields(m), " ")))
	}
	// SET is blocked except inside EXPLAIN (e.g. EXPLAIN (SETTINGS ...) is legit).
	if !strings.HasPrefix(upper, "EXPLAIN") && setRe.MatchString(analysis) {
		return fmt.Errorf("blocked keyword %q — gateway is read-only", "SET")
	}
	// forbid stacked statements like "SELECT 1; DROP ..."
	trimmed := strings.TrimSuffix(strings.TrimSpace(analysis), ";")
	if multiStatement.MatchString(trimmed+";") || strings.Count(trimmed, ";") > 0 && containsSecondStatement(trimmed) {
		return fmt.Errorf("multiple statements are not allowed — send one query at a time")
	}
	// forbid writes hidden in CTEs: WITH ... INSERT/UPDATE/DELETE
	if strings.Contains(upper, "WITH") && writeCteRe.MatchString(analysis) {
		return fmt.Errorf("data-modifying CTEs are not allowed")
	}
	return nil
}

func containsSecondStatement(s string) bool {
	// crude: a semicolon followed by non-space means stacked
	parts := strings.Split(s, ";")
	if len(parts) <= 1 {
		return false
	}
	for _, p := range parts[1:] {
		if strings.TrimSpace(p) != "" {
			return true
		}
	}
	return false
}

// writeDeny blocks everything that is not plain-row DML: DDL, stacked
// statements, procedures, bulk loaders, pragmas, and raw device paths.
var writeDeny = regexp.MustCompile(`(?i)\b(CREATE|ALTER|DROP|TRUNCATE|GRANT|REVOKE|VACUUM|ANALYZE|CLUSTER|REINDEX|CALL|DO|LISTEN|NOTIFY|LOAD|RESET|BEGIN|COMMIT|ROLLBACK|SAVEPOINT|EXEC(UTE)?|PERFORM|PRAGMA|ATTACH|DETACH|COPY|INTO\s+OUTFILE|INTO\s+DUMPFILE|SECURITY\s+DEFINER|PG_TERMINATE|PG_CANCEL|DBLINK)\b`)

// ValidateWriteSQL enforces the app-write invariant: exactly one
// INSERT, UPDATE, or DELETE statement. SELECT goes through the read path;
// DDL never executes from the app. Use with NeedsWhere for full-table risk.
func ValidateWriteSQL(raw string) error {
	sql := strings.TrimSpace(raw)
	if sql == "" {
		return fmt.Errorf("empty statement")
	}
	noComments := commentStrip.ReplaceAllString(sql, " ")
	upper := strings.ToUpper(strings.TrimSpace(noComments))
	allowedPrefix := false
	for _, p := range []string{"INSERT", "UPDATE", "DELETE"} {
		if strings.HasPrefix(upper, p+" ") || strings.HasPrefix(upper, p+"(") || upper == p {
			allowedPrefix = true
			break
		}
	}
	// INSERT INTO t ... / UPDATE t ... / DELETE FROM t ... (any spacing)
	if !allowedPrefix {
		for _, p := range []string{"INSERT INTO", "INSERT ", "UPDATE ", "DELETE FROM", "DELETE "} {
			if strings.HasPrefix(upper, p) {
				allowedPrefix = true
				break
			}
		}
	}
	if !allowedPrefix {
		return fmt.Errorf("only single INSERT / UPDATE / DELETE statements are allowed (SELECT uses the read path, DDL is blocked)")
	}
	analysis := stringLitStrip.ReplaceAllString(noComments, "''")
	if m := writeDeny.FindString(analysis); m != "" {
		return fmt.Errorf("blocked keyword %q — app writes are row-DML only", strings.ToUpper(strings.Join(strings.Fields(m), " ")))
	}
	trimmed := strings.TrimSuffix(strings.TrimSpace(analysis), ";")
	if multiStatement.MatchString(trimmed+";") || containsSecondStatement(trimmed) {
		return fmt.Errorf("multiple statements are not allowed — send one statement at a time")
	}
	return nil
}

// NeedsWhere reports whether an UPDATE/DELETE lacks a WHERE clause
// (outside of string literals). Callers must get explicit confirmation
// before running such a statement.
func NeedsWhere(raw string) bool {
	noComments := commentStrip.ReplaceAllString(raw, " ")
	analysis := stringLitStrip.ReplaceAllString(noComments, "''")
	upper := strings.ToUpper(strings.TrimSpace(analysis))
	if !(strings.HasPrefix(upper, "UPDATE ") || strings.HasPrefix(upper, "DELETE ")) {
		return false
	}
	return !regexp.MustCompile(`(?i)\bWHERE\b`).MatchString(analysis)
}

// EnforceLimit appends a LIMIT when the query has none, to protect the fleet.
func EnforceLimit(sql string, limit int) string {
	upper := strings.ToUpper(sql)
	if strings.Contains(upper, "LIMIT") {
		return sql
	}
	if !strings.HasPrefix(strings.ToUpper(strings.TrimSpace(sql)), "SELECT") &&
		!strings.HasPrefix(strings.ToUpper(strings.TrimSpace(sql)), "WITH") {
		return sql
	}
	s := strings.TrimSuffix(strings.TrimSpace(sql), ";")
	return fmt.Sprintf("%s LIMIT %d", s, limit)
}
