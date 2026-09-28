package store

import "testing"

func TestWriteUsers(t *testing.T) {
	st := testStore(t)
	if n := st.WriteUserName("s1"); n != "" {
		t.Fatal("name must be empty")
	}
	st.SaveWriteUser("s1", "postgres", "s3cret!")
	u, p, ok := st.GetWriteUser("s1")
	if !ok || u != "postgres" || p != "s3cret!" {
		t.Fatalf("round-trip: %q %q %v", u, p, ok)
	}
	if n := st.WriteUserName("s1"); n != "postgres" {
		t.Fatalf("name: %q", n)
	}
	// replace
	st.SaveWriteUser("s1", "root", "x")
	if u, _, _ := st.GetWriteUser("s1"); u != "root" {
		t.Fatalf("replace: %q", u)
	}
	// clearing via empty password
	st.SaveWriteUser("s1", "root", "")
	if _, _, ok := st.GetWriteUser("s1"); ok {
		t.Fatal("empty password must clear")
	}
	st.SaveWriteUser("s1", "postgres", "s3cret!")
	st.DeleteWriteUser("s1")
	if _, _, ok := st.GetWriteUser("s1"); ok {
		t.Fatal("delete must clear")
	}
}
