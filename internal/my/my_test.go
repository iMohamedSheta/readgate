package my

import (
	"strings"
	"testing"

	"readgate/internal/model"
)

func TestDSN(t *testing.T) {
	d := dsn("127.0.0.1", 3306, "app", "ai", "p@ss:word")
	if !strings.Contains(d, "tcp(127.0.0.1:3306)") || !strings.Contains(d, "/app?") {
		t.Fatalf("dsn: %s", d)
	}
}

func TestBuildWhereMySQL(t *testing.T) {
	w, args := buildWhere([]model.Filter{
		{Column: "email", Op: "contains", Value: "a%b_c", Logic: "AND"},
		{Column: "age", Op: "gte", Value: "18", Logic: "AND"},
		{Column: "id", Op: "in", Value: "1, 2", Logic: "OR"},
		{Column: "x", Op: "bogus", Value: "1", Logic: "AND"},
		{Column: "bad col", Op: "eq", Value: "1", Logic: "AND"},
	}, "")
	if !strings.Contains(w, "`email` LIKE ?") || !strings.Contains(w, "`age` >= ?") {
		t.Fatalf("where: %s", w)
	}
	if !strings.Contains(w, "OR `id` IN (?,?)") {
		t.Fatalf("in/logic: %s", w)
	}
	if strings.Contains(w, "bogus") || strings.Contains(w, "bad col") {
		t.Fatalf("unsafe passthrough: %s", w)
	}
	if len(args) != 4 {
		t.Fatalf("args: %v", args)
	}
	if _, ok := args[1].(int64); !ok {
		t.Fatalf("coerce int: %#v", args[1])
	}
}

func TestRawDeny(t *testing.T) {
	if _, args := buildWhere(nil, "a = 1; DROP TABLE t"); len(args) != 0 {
		t.Fatal("stacked raw must be dropped")
	}
	w, _ := buildWhere(nil, "status = 'paid'")
	if !strings.Contains(w, "status = 'paid'") {
		t.Fatalf("clean raw kept: %s", w)
	}
}

func TestProvisionSQL(t *testing.T) {
	s := ProvisionSQL("shop", "ai_readonly", "s3cret")
	for _, want := range []string{"CREATE USER", "GRANT SELECT, SHOW VIEW", "`shop`", "FLUSH PRIVILEGES"} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in:\n%s", want, s)
		}
	}
}
