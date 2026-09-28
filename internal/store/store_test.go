package store

import (
	"os"
	"testing"

	"readgate/internal/model"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	dir, err := os.MkdirTemp("", "readgate-test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	t.Setenv("READGATE_HOME", dir)
	st, err := New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// Fresh DB starts EMPTY — no demo data. CRUD must persist in SQLite.
func TestCRUD(t *testing.T) {
	st := testStore(t)

	if seeded := st.ListClusters(); len(seeded) != 0 {
		t.Fatalf("fresh store must be empty, got %d clusters", len(seeded))
	}

	c := st.SaveCluster(model.Cluster{Name: "Staging", Description: "pre-prod", Color: "#ec4899"})
	if c.ID == "" {
		t.Fatal("cluster id not assigned")
	}
	if len(st.ListClusters()) != 1 {
		t.Fatal("cluster not persisted")
	}

	s := st.SaveSource(model.Source{
		Name: "StageDB", ClusterID: c.ID, Engine: model.EnginePostgres, Mode: model.ModeSSH,
		Host: "127.0.0.1", Database: "app", Username: "ai_readonly", Password: "s3cret!",
		SSHHost: "10.0.0.1", SSHUser: "ubuntu", SSHAuth: "password",
		SSHKeyPath: "~/.ssh/id", SSHPassword: "ssh-s3cret",
	})
	if s.ID == "" {
		t.Fatal("source id not assigned")
	}
	if s.Port != 5432 || s.SSHPort != 22 {
		t.Fatalf("port defaults not applied: %+v", s)
	}

	got, ok := st.GetSource(s.ID)
	if !ok {
		t.Fatal("saved source not found")
	}
	if got.Password != "s3cret!" {
		t.Fatal("password did not survive encrypted round-trip")
	}
	if got.SSHPassword != "ssh-s3cret" || got.SSHAuth != "password" {
		t.Fatalf("ssh auth did not survive round-trip: %+v", got)
	}
	if got.CreatedAt == "" {
		t.Fatal("createdAt not set")
	}

	// raw column must NOT contain the plaintext password
	var enc string
	if err := st.db.QueryRow(`SELECT password_enc FROM sources WHERE id=?`, s.ID).Scan(&enc); err != nil {
		t.Fatal(err)
	}
	if enc == "s3cret!" || enc == "" {
		t.Fatalf("password stored in plaintext: %q", enc)
	}

	st.DeleteSource(s.ID)
	if _, ok := st.GetSource(s.ID); ok {
		t.Fatal("source not deleted")
	}
	st.DeleteCluster(c.ID)
	if len(st.ListClusters()) != 0 {
		t.Fatal("cluster not deleted")
	}
}

// Old demo rows (TEST-NET-3 IPs) are cleaned up on open.
func TestFakeCleanup(t *testing.T) {
	st := testStore(t)
	c := st.SaveCluster(model.Cluster{Name: "Production", Description: "Customer-facing fleet", Color: "#10b981"})
	st.SaveSource(model.Source{Name: "FakeDB", ClusterID: c.ID, Engine: model.EnginePostgres,
		Mode: model.ModeSSH, Database: "x", Username: "ai_readonly", Password: "pw",
		SSHHost: "203.0.113.10", SSHUser: "ubuntu", SSHKeyPath: "k"})
	st.Close()

	t.Setenv("READGATE_HOME", AppDir())
	st2, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	if got := st2.ListSources(); len(got) != 0 {
		t.Fatalf("fake source survived: %+v", got)
	}
	if got := st2.ListClusters(); len(got) != 0 {
		t.Fatalf("empty demo cluster survived: %+v", got)
	}
}

// Legacy store.json must import once into SQLite.
func TestLegacyImport(t *testing.T) {
	dir, err := os.MkdirTemp("", "readgate-legacy")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	t.Setenv("READGATE_HOME", dir)

	legacy := `{"clusters":[{"id":"c1","name":"Old","description":"","color":"#fff","createdAt":"2026-01-01T00:00:00Z"}],
		"sources":[{"id":"s1","name":"OldDB","clusterId":"c1","engine":"postgres","mode":"direct","host":"h","port":5432,
		"database":"d","username":"ai_readonly","password":"plain:pw","status":"ready","readOnlyVerified":true,"createdAt":"2026-01-01T00:00:00Z"}]}`
	if err := os.WriteFile(dir+"/store.json", []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	src, ok := st.GetSource("s1")
	if !ok || src.Password != "pw" || src.Name != "OldDB" {
		t.Fatalf("legacy import failed: %+v %v", src, ok)
	}
	if _, err := os.Stat(dir + "/store.json"); !os.IsNotExist(err) {
		t.Fatal("legacy store.json should be archived after import")
	}
}
