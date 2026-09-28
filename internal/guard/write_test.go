package guard

import "testing"

func TestValidateWriteSQL(t *testing.T) {
	for _, q := range []string{
		"INSERT INTO t (a) VALUES (1)",
		"UPDATE t SET a = 1 WHERE id = 2",
		"DELETE FROM t WHERE id = 2",
		"insert into t values (1);",
		"DELETE FROM t WHERE id = 1; -- trailing comment",
	} {
		if err := ValidateWriteSQL(q); err != nil {
			t.Fatalf("must accept %q: %v", q, err)
		}
	}
	for _, q := range []string{
		"",
		"SELECT * FROM t",
		"DROP TABLE t",
		"ALTER TABLE t ADD COLUMN x INT",
		"UPDATE t SET a = 1; DELETE FROM t",
		"EXEC sp_who",
		"WITH x AS (SELECT 1) DELETE FROM t",
		"INSERT INTO t SELECT * FROM o",
	} {
		err := ValidateWriteSQL(q)
		// INSERT..SELECT is legit DML and must pass; the rest must fail.
		if q == "INSERT INTO t SELECT * FROM o" {
			if err != nil {
				t.Fatalf("must accept %q: %v", q, err)
			}
			continue
		}
		if err == nil {
			t.Fatalf("must reject %q", q)
		}
	}
}

func TestValidateWriteSmuggledDDL(t *testing.T) {
	if err := ValidateWriteSQL("UPDATE t SET a = 'x CREATE TABLE y' WHERE id = 1"); err != nil {
		t.Fatalf("quoted text is data: %v", err)
	}
}

func TestNeedsWhere(t *testing.T) {
	if !NeedsWhere("UPDATE t SET a = 1") {
		t.Fatal("bare UPDATE needs WHERE")
	}
	if !NeedsWhere("DELETE FROM t") {
		t.Fatal("bare DELETE needs WHERE")
	}
	if NeedsWhere("UPDATE t SET a = 1 WHERE id = 2") {
		t.Fatal("guarded UPDATE is fine")
	}
	if NeedsWhere("INSERT INTO t (a) VALUES (1)") {
		t.Fatal("INSERT never needs WHERE")
	}
	if NeedsWhere("DELETE FROM t WHERE note = 'where is here'") {
		t.Fatal("quoted where is data, real WHERE present")
	}
}
