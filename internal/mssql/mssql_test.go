package mssql

import (
	"strings"
	"testing"

	"readgate/internal/model"
)

func TestTopInject(t *testing.T) {
	if got := top("SELECT a FROM t", 50); got != "SELECT TOP (50) a FROM t" {
		t.Fatalf("top: %s", got)
	}
	if got := top("select distinct a from t", 10); !strings.Contains(got, "TOP (10)") {
		t.Fatalf("distinct: %s", got)
	}
	if got := top("WITH x AS (SELECT 1) SELECT * FROM x", 10); got != "WITH x AS (SELECT 1) SELECT * FROM x" {
		t.Fatalf("cte untouched: %s", got)
	}
}

func TestExecDenied(t *testing.T) {
	if err := checkSQL("EXEC sp_who"); err == nil {
		t.Fatal("EXEC must be rejected")
	}
	if err := checkSQL("SELECT * FROM t; EXEC xp_cmdshell 'x'"); err == nil {
		t.Fatal("stacked exec must be rejected")
	}
	if err := checkSQL("SELECT * FROM [orders]"); err != nil {
		t.Fatalf("plain select: %v", err)
	}
}

func TestBuildWhereMSSQL(t *testing.T) {
	w, args := buildWhere([]model.Filter{
		{Column: "email", Op: "contains", Value: "x", Logic: "AND"},
		{Column: "age", Op: "in", Value: "1,2", Logic: "OR"},
	}, "")
	if !strings.Contains(w, "[email] LIKE @p1") || !strings.Contains(w, "OR [age] IN (@p2,@p3)") {
		t.Fatalf("where: %s args %v", w, args)
	}
}

func TestProvisionSQL(t *testing.T) {
	s := ProvisionSQL("shop", "ai_readonly", "pw")
	for _, want := range []string{"CREATE LOGIN", "db_datareader", "[shop]"} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q:\n%s", want, s)
		}
	}
}
