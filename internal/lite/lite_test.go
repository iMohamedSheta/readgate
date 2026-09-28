package lite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"readgate/internal/model"

	_ "modernc.org/sqlite"
)

func testDB(t *testing.T) model.Source {
	t.Helper()
	p := filepath.Join(t.TempDir(), "t.db")
	db, err := sql.Open("sqlite", "file:"+p)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, s := range []string{
		`CREATE TABLE users(id INTEGER PRIMARY KEY, email TEXT NOT NULL, age INTEGER)`,
		`CREATE TABLE orders(id INTEGER PRIMARY KEY, user_id INTEGER REFERENCES users(id), total REAL)`,
		`CREATE INDEX idx_orders_user ON orders(user_id)`,
		`INSERT INTO users(email, age) VALUES ('a@x.com', 30), ('b@x.com', 25)`,
		`INSERT INTO orders(user_id, total) VALUES (1, 99.5), (2, 10.0)`,
	} {
		if _, err := db.Exec(s); err != nil {
			t.Fatal(err)
		}
	}
	return model.Source{Name: "t", Engine: model.EngineSQLite, Mode: model.ModeFile, Database: p}
}

func TestVerify(t *testing.T) {
	src := testDB(t)
	checks := VerifyReadOnly(context.Background(), src)
	for _, c := range checks {
		if (c.Key == "select" || c.Key == "write" || c.Key == "ro") && !c.OK {
			t.Fatalf("check %s failed: %s", c.Key, c.Detail)
		}
	}
}

func TestMissingFile(t *testing.T) {
	src := model.Source{Name: "x", Engine: model.EngineSQLite, Mode: model.ModeFile, Database: filepath.Join(t.TempDir(), "nope.db")}
	checks := VerifyReadOnly(context.Background(), src)
	failed := false
	for _, c := range checks {
		if (c.Key == "select" || c.Key == "write" || c.Key == "ro") && !c.OK {
			failed = true
		}
	}
	if !failed {
		t.Fatal("missing file must fail the enable invariant")
	}
}

func TestSchemaColumnsQuery(t *testing.T) {
	ctx := context.Background()
	src := testDB(t)
	sch, err := Schema(ctx, src)
	if err != nil {
		t.Fatal(err)
	}
	if len(sch.Tables) != 2 {
		t.Fatalf("want 2 tables, got %d", len(sch.Tables))
	}
	cols, err := Columns(ctx, src, "main", "users")
	if err != nil || len(cols) != 3 {
		t.Fatalf("cols: %v %v", cols, err)
	}
	res, err := Query(ctx, src, "SELECT email FROM users ORDER BY 1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if res.RowCount != 2 || res.Columns[0] != "email" {
		t.Fatalf("query: %+v", res)
	}
	if _, err := Query(ctx, src, "DROP TABLE users", 10); err == nil {
		t.Fatal("writes must be rejected")
	}
	pv, err := Preview(ctx, src, "main", "orders", 50, 0, []model.Filter{{Column: "total", Op: "gt", Value: "50", Logic: "AND"}}, "")
	if err != nil || pv.RowCount != 1 {
		t.Fatalf("preview: %+v %v", pv, err)
	}
	plan, err := Explain(ctx, src, "SELECT * FROM orders WHERE total > 1")
	if err != nil || plan == "" {
		t.Fatalf("explain: %q %v", plan, err)
	}
}

func TestExecWrite(t *testing.T) {
	ctx := context.Background()
	src := testDB(t)
	if n, err := ExecWrite(ctx, src, "", "", "INSERT INTO users(email, age) VALUES ('c@x.com', 40)", false); err != nil || n != 1 {
		t.Fatalf("insert: %d %v", n, err)
	}
	if n, err := ExecWrite(ctx, src, "", "", "UPDATE users SET age = 31 WHERE email = 'a@x.com'", false); err != nil || n != 1 {
		t.Fatalf("update: %d %v", n, err)
	}
	if _, err := ExecWrite(ctx, src, "", "", "DELETE FROM users", false); err == nil {
		t.Fatal("full-table delete needs confirmation")
	}
	if n, err := ExecWrite(ctx, src, "", "", "DELETE FROM users WHERE email = 'b@x.com'", false); err != nil || n != 1 {
		t.Fatalf("delete: %d %v", n, err)
	}
	for _, bad := range []string{"DROP TABLE users", "SELECT * FROM users", "UPDATE users SET age = 1; DELETE FROM users"} {
		if _, err := ExecWrite(ctx, src, "", "", bad, true); err == nil {
			t.Fatalf("must reject %q", bad)
		}
	}
	res, err := Query(ctx, src, "SELECT COUNT(*) AS c FROM users", 10)
	if err != nil || len(res.Rows) != 1 {
		t.Fatalf("count: %+v %v", res, err)
	}
}

func TestTools(t *testing.T) {
	ctx := context.Background()
	src := testDB(t)
	if _, err := DoctorFindings(ctx, src); err != nil {
		t.Fatal(err)
	}
	if _, err := TableStats(ctx, src, "", "", 10); err != nil {
		t.Fatal(err)
	}
	ix, err := Indexes(ctx, src, "main", "orders")
	if err != nil || len(ix) == 0 {
		t.Fatalf("indexes: %v %v", ix, err)
	}
	rel, err := Relationships(ctx, src, "main", "orders", 10)
	if err != nil || len(rel) == 0 {
		t.Fatalf("relationships: %v %v", rel, err)
	}
	m, err := SearchTables(ctx, src, "ord", 10)
	if err != nil || len(m) == 0 {
		t.Fatalf("search: %v %v", m, err)
	}
	if _, err := SlowQueries(ctx, src, 5); err != nil {
		t.Fatal(err)
	}
}
