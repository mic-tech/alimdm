package store

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// A deployment created before roles existed has operators(email, password_hash)
// only. Opening that database must add the new columns and leave the existing
// account able to administer the console.
func TestMigrateLegacyOperatorsTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE operators(email TEXT PRIMARY KEY, password_hash TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO operators VALUES('Legacy@Example.com','hash')`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	st, err := Open(path)
	if err != nil {
		t.Fatalf("open/migrate: %v", err)
	}
	defer st.Close()

	op, err := st.GetOperator("legacy@example.com") // lookup is case-insensitive
	if err != nil {
		t.Fatalf("legacy operator missing after migration: %v", err)
	}
	if op.Role != RoleAdmin {
		t.Errorf("legacy operator role = %q, want %q (upgrade would lock out account management)", op.Role, RoleAdmin)
	}
	if op.PasswordHash != "hash" {
		t.Errorf("password hash altered by migration: %q", op.PasswordHash)
	}
}

// Migrating twice must be a no-op rather than an error.
func TestMigrateIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "twice.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	st.Close()
	st2, err := Open(path)
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	st2.Close()
}

// A fresh database seeded with an operator-role account must not be silently
// promoted — the promotion only rescues databases that predate roles.
func TestNewAccountsKeepTheirRole(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "fresh.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if err := st.UpsertOperator(&Operator{Email: "a@x.com", Role: RoleAdmin, PasswordHash: "h"}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateOperator(&Operator{Email: "b@x.com", Role: RoleOperator, PasswordHash: "h"}); err != nil {
		t.Fatal(err)
	}
	op, err := st.GetOperator("b@x.com")
	if err != nil {
		t.Fatal(err)
	}
	if op.Role != RoleOperator {
		t.Errorf("role = %q, want %q", op.Role, RoleOperator)
	}

	n, err := st.CountAdminsExcept("a@x.com")
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("CountAdminsExcept(only admin) = %d, want 0", n)
	}
}

// Creating a duplicate account must fail rather than overwrite the existing one.
func TestCreateOperatorRejectsDuplicate(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "dup.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	o := &Operator{Email: "dup@x.com", Role: RoleOperator, PasswordHash: "first"}
	if err := st.CreateOperator(o); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateOperator(&Operator{Email: "dup@x.com", Role: RoleAdmin, PasswordHash: "second"}); err == nil {
		t.Fatal("duplicate CreateOperator succeeded, want error")
	}
	got, _ := st.GetOperator("dup@x.com")
	if got.PasswordHash != "first" {
		t.Errorf("existing account was overwritten: hash = %q", got.PasswordHash)
	}
}
