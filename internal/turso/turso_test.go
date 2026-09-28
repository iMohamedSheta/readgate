package turso

import (
	"context"
	"testing"

	"readgate/internal/model"
)

func TestValidURL(t *testing.T) {
	for _, u := range []string{"libsql://db-org.turso.io", "https://x.com", "http://127.0.0.1:8080"} {
		if !ValidURL(u) {
			t.Fatalf("must accept %s", u)
		}
	}
	for _, u := range []string{"", "db.turso.io", "ftp://x", "libsql://"} {
		if ValidURL(u) {
			t.Fatalf("must reject %q", u)
		}
	}
}

func TestBadURLFailsInvariant(t *testing.T) {
	src := model.Source{Name: "x", Engine: model.EngineTurso, Mode: model.ModeDirect, Database: "nope"}
	checks := VerifyReadOnly(context.Background(), src)
	failed := false
	for _, c := range checks {
		if (c.Key == "select" || c.Key == "write" || c.Key == "ro") && !c.OK {
			failed = true
		}
	}
	if !failed {
		t.Fatal("bad URL must fail the enable invariant")
	}
}
