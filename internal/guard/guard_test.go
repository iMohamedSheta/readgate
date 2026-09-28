package guard

import "testing"

func TestReadOnly(t *testing.T) {
	ok := []string{
		"SELECT 1",
		"SELECT * FROM public.orders ORDER BY created_at DESC",
		"SELECT id, updated_at, deleted_at, granted_by FROM public.orders",
		"SELECT * FROM cluster_stats WHERE reindexed = false",
		"SELECT * FROM t WHERE name='Grant' AND note='do not drop it'",
		"WITH x AS (SELECT 1) SELECT * FROM x",
		"WITH recent AS (SELECT * FROM public.orders) SELECT * FROM recent",
		"EXPLAIN SELECT * FROM users",
		"EXPLAIN (ANALYZE, BUFFERS) SELECT * FROM users",
		"SHOW default_transaction_read_only",
		"TABLE public.orders",
	}
	for _, q := range ok {
		if err := ValidateReadOnlySQL(q); err != nil {
			t.Fatalf("should allow %q: %v", q, err)
		}
	}
	blocked := []string{
		"DROP TABLE users",
		"SELECT 1; DROP TABLE users",
		"WITH x AS (SELECT 1) DELETE FROM users",
		"WITH d AS (DELETE FROM users RETURNING *) SELECT * FROM d",
		"INSERT INTO users VALUES (1)",
		"UPDATE users SET x=1",
		"CREATE TABLE x(id int)",
		"ALTER TABLE x ADD COLUMN y int",
		"GRANT SELECT ON users TO x",
		"TRUNCATE users",
		"SET statement_timeout='1s'",
		"BEGIN",
		"DO $$ BEGIN RAISE NOTICE 'x'; END $$",
		"COPY users FROM '/tmp/x'",
	}
	for _, q := range blocked {
		if err := ValidateReadOnlySQL(q); err == nil {
			t.Fatalf("should block %q", q)
		}
	}
}
