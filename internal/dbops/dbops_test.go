package dbops

import (
	"context"
	"testing"

	"readgate/internal/model"
)

func TestNotBundledFailsLoudly(t *testing.T) {
	src := model.Source{Name: "x", Engine: model.EngineMySQL, Mode: model.ModeDirect}
	if _, err := Schema(context.Background(), src); err == nil {
		t.Fatal("mysql without driver must error, never silently use another dialect")
	}
	checks := VerifyReadOnly(context.Background(), src, "u", "p")
	failed := false
	for _, c := range checks {
		if (c.Key == "select" || c.Key == "write" || c.Key == "ro") && !c.OK {
			failed = true
		}
	}
	if !failed {
		t.Fatal("unbundled engine must fail the enable invariant")
	}
}

func TestUnknownEngineFallsBackToPostgresFamily(t *testing.T) {
	src := model.Source{Name: "x", Engine: "", Mode: model.ModeDirect, Host: "127.0.0.1", Port: 1, Database: "db"}
	_, err := Schema(context.Background(), src)
	if err == nil {
		t.Fatal("expected connection failure, not a routing failure")
	}
}
