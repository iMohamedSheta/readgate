package pg

import (
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Passwords with URL-significant chars must survive the DSN round-trip.
// Unencoded, "p@ss:w?rd#1" gets mangled and every login fails with 28P01.
func TestDSNEncoding(t *testing.T) {
	dsn := dsnFor("127.0.0.1", 5432, "waffiy", "ai_readonly", `p@ss:w?rd#1&x`)
	for _, raw := range []string{"p@ss", "w?rd", "#1"} {
		if strings.Contains(dsn, raw) {
			t.Fatalf("dsn leaks unencoded credential part %q: %s", raw, dsn)
		}
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ConnConfig.Password != `p@ss:w?rd#1&x` {
		t.Fatalf("password mangled: %q", cfg.ConnConfig.Password)
	}
	if cfg.ConnConfig.User != "ai_readonly" || cfg.ConnConfig.Database != "waffiy" {
		t.Fatalf("user/db mangled: %+v", cfg.ConnConfig)
	}
}
