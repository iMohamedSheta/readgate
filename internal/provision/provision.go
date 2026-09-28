// Package provision generates read-only identity setup SQL.
package provision

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"strings"
)

const alphabet = "abcdefghjkmnpqrstuvwxyzABCDEFGHJKMNPQRSTUVWXYZ23456789_-"

// RandomPassword generates a strong password for the ai_readonly role.
func RandomPassword(n int) string {
	if n <= 0 {
		n = 28
	}
	out := make([]byte, n)
	for i := range out {
		idx, _ := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		out[i] = alphabet[idx.Int64()]
	}
	return string(out)
}

func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

func quoteLiteral(s string) string {
	return `'` + strings.ReplaceAll(s, `'`, `''`) + `'`
}

// GenerateReadOnlySQL returns the idempotent setup script run with admin creds.
// It is intentionally explicit so users can audit it in Mode B (manual).
func GenerateReadOnlySQL(dbName, aiUser, aiPassword string) string {
	u := quoteIdent(aiUser)
	return fmt.Sprintf(`-- ReadGate · read-only AI role for database %s
-- Run as an administrator (e.g. postgres). Safe to re-run.

CREATE ROLE %s WITH LOGIN PASSWORD %s;

GRANT CONNECT ON DATABASE %s TO %s;

GRANT USAGE ON SCHEMA public TO %s;

GRANT SELECT ON ALL TABLES IN SCHEMA public TO %s;

ALTER DEFAULT PRIVILEGES FOR ROLE CURRENT_USER IN SCHEMA public
  GRANT SELECT ON TABLES TO %s;

-- Future-proof: also cover tables created by postgres explicitly
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'postgres') THEN
    EXECUTE format('ALTER DEFAULT PRIVILEGES FOR ROLE postgres IN SCHEMA public GRANT SELECT ON TABLES TO %%s', %s);
  END IF;
END $$;

ALTER ROLE %s SET default_transaction_read_only = on;

-- Verification (run as %s afterwards):
--   SELECT has_database_privilege(current_user, %s, 'CONNECT');
--   SELECT 1;
`,
		quoteIdent(dbName),
		u, quoteLiteral(aiPassword),
		quoteIdent(dbName), u,
		u,
		u,
		u, quoteLiteral(aiUser),
		u,
		u, quoteLiteral(dbName),
	)
}

// VerifyQueries are executed as the ai user to PROVE read-only before enabling.
func VerifyQueries(dbName string) []string {
	return []string{
		`SELECT 1 AS gateway_probe`,
		fmt.Sprintf(`SELECT has_database_privilege(current_user, '%s', 'CONNECT') AS can_connect`,
			strings.ReplaceAll(dbName, `'`, `''`)),
		`SELECT current_setting('default_transaction_read_only', true) AS ro_flag`,
	}
}

// ManualScript produces a copy-paste bundle for Mode B users.
func ManualScript(dbHost string, dbPort int, dbName, adminUser, aiUser, aiPassword string) string {
	sql := GenerateReadOnlySQL(dbName, aiUser, aiPassword)
	return fmt.Sprintf(`#!/usr/bin/env bash
# ReadGate — manual read-only user setup (Mode B)
# You run this. The gateway never sees your admin password.
#
#   chmod +x setup-%s.sh && ./setup-%s.sh
#
set -euo pipefail

export PGHOST=%s PGPORT=%d PGDATABASE=%s PGUSER=%s
echo "Paste the SQL below into psql (or pipe it):"
echo "----------------------------------------"
cat <<'SQL'
%s
----------------------------------------
psql -v ON_ERROR_STOP=1 <<'SQL'
%sSQL

echo "Done. Now add this source in ReadGate with:"
echo "  database user: %s"
echo "  (the generated password — admin password is NOT stored)"
`, aiUser, aiUser, dbHost, dbPort, dbName, adminUser, sql, sql, aiUser)
}
