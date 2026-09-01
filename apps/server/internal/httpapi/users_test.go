package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"ali-mdm/server/internal/auth"
	"ali-mdm/server/internal/blob"
	"ali-mdm/server/internal/store"
)

type testEnv struct {
	t   *testing.T
	mux *http.ServeMux
	st  *store.Store
	srv *Server
}

// testGroup is the minimal group a device needs to heartbeat.
var testGroup = store.Group{
	ID: "default", Name: "Default", Config: "{}", ConfigHash: "h", ConfigVersion: 1,
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	hash, _ := auth.HashPassword("adminpassword")
	if err := st.UpsertOperator(&store.Operator{
		Email: "admin@x.com", Name: "Admin", Role: store.RoleAdmin,
		PasswordHash: hash, CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		t.Fatal(err)
	}

	// A real file store: the inbox tests move actual bytes, and a nil store
	// would make them pass by refusing every upload.
	files := blob.NewStore(filepath.Join(t.TempDir(), "files"))
	srv := New(st, auth.NewSigner("test-secret"), nil, nil, files, NewPokeQueue(), "enroll", "http://x", "")
	return &testEnv{t: t, mux: srv.Routes(), st: st, srv: srv}
}

func (e *testEnv) do(method, path, token string, body any) (*httptest.ResponseRecorder, map[string]any) {
	e.t.Helper()
	var buf bytes.Buffer
	if body != nil {
		json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req)
	out := map[string]any{}
	json.Unmarshal(rec.Body.Bytes(), &out)
	return rec, out
}

func (e *testEnv) login(email, password string) string {
	e.t.Helper()
	rec, body := e.do("POST", "/api/v1/operator/login", "", map[string]string{
		"email": email, "password": password,
	})
	if rec.Code != http.StatusOK {
		e.t.Fatalf("login %s: status %d (%s)", email, rec.Code, rec.Body.String())
	}
	tok, _ := body["token"].(string)
	if tok == "" {
		e.t.Fatalf("login %s returned no token", email)
	}
	return tok
}

// An operator-role account must not reach any account-administration endpoint.
func TestOperatorRoleCannotAdministerUsers(t *testing.T) {
	e := newTestEnv(t)
	adminTok := e.login("admin@x.com", "adminpassword")

	if rec, _ := e.do("POST", "/api/v1/users", adminTok, map[string]string{
		"email": "op@x.com", "name": "Op", "role": "operator", "password": "operatorpw1",
	}); rec.Code != http.StatusCreated {
		t.Fatalf("create user: status %d (%s)", rec.Code, rec.Body.String())
	}

	opTok := e.login("op@x.com", "operatorpw1")

	for _, c := range []struct{ method, path string }{
		{"GET", "/api/v1/users"},
		{"POST", "/api/v1/users"},
		{"PUT", "/api/v1/users/admin%40x.com"},
		{"DELETE", "/api/v1/users/admin%40x.com"},
		{"POST", "/api/v1/users/admin%40x.com/password"},
	} {
		rec, _ := e.do(c.method, c.path, opTok, map[string]string{"role": "admin", "new_password": "hijacked123"})
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s %s as operator: status %d, want 403", c.method, c.path, rec.Code)
		}
	}

	// The operator must still be able to reach its own profile.
	if rec, _ := e.do("GET", "/api/v1/me", opTok, nil); rec.Code != http.StatusOK {
		t.Errorf("GET /me as operator: status %d, want 200", rec.Code)
	}
}

// The console must never be left without an administrator.
func TestLastAdminIsProtected(t *testing.T) {
	e := newTestEnv(t)
	tok := e.login("admin@x.com", "adminpassword")

	rec, body := e.do("PUT", "/api/v1/users/admin%40x.com", tok, map[string]string{"role": "operator"})
	if rec.Code != http.StatusConflict {
		t.Errorf("demoting last admin: status %d, want 409", rec.Code)
	}
	if body["error"] == nil {
		t.Error("expected an error message explaining the refusal")
	}

	// Deleting yourself is refused outright.
	if rec, _ := e.do("DELETE", "/api/v1/users/admin%40x.com", tok, nil); rec.Code != http.StatusConflict {
		t.Errorf("deleting own account: status %d, want 409", rec.Code)
	}

	// With a second admin present, demotion is allowed again.
	e.do("POST", "/api/v1/users", tok, map[string]string{
		"email": "admin2@x.com", "role": "admin", "password": "admin2password",
	})
	if rec, _ := e.do("PUT", "/api/v1/users/admin%40x.com", tok, map[string]string{"role": "operator"}); rec.Code != http.StatusOK {
		t.Errorf("demoting with a spare admin: status %d, want 200", rec.Code)
	}
}

// Role changes must take effect immediately, not at token expiry.
func TestDemotionAppliesToExistingSession(t *testing.T) {
	e := newTestEnv(t)
	tok := e.login("admin@x.com", "adminpassword")
	e.do("POST", "/api/v1/users", tok, map[string]string{
		"email": "admin2@x.com", "role": "admin", "password": "admin2password",
	})
	tok2 := e.login("admin2@x.com", "admin2password")

	// admin2 still holds a token minted while it was an admin.
	if rec, _ := e.do("GET", "/api/v1/users", tok2, nil); rec.Code != http.StatusOK {
		t.Fatalf("admin2 listing users: status %d, want 200", rec.Code)
	}
	e.do("PUT", "/api/v1/users/admin2%40x.com", tok, map[string]string{"role": "operator"})
	if rec, _ := e.do("GET", "/api/v1/users", tok2, nil); rec.Code != http.StatusForbidden {
		t.Errorf("demoted admin reusing old token: status %d, want 403", rec.Code)
	}
}

// Deleting an account must invalidate its live session.
func TestDeletedUserSessionIsRejected(t *testing.T) {
	e := newTestEnv(t)
	tok := e.login("admin@x.com", "adminpassword")
	e.do("POST", "/api/v1/users", tok, map[string]string{
		"email": "gone@x.com", "role": "operator", "password": "goneuserpw1",
	})
	goneTok := e.login("gone@x.com", "goneuserpw1")

	if rec, _ := e.do("GET", "/api/v1/me", goneTok, nil); rec.Code != http.StatusOK {
		t.Fatalf("precondition: status %d, want 200", rec.Code)
	}
	if rec, _ := e.do("DELETE", "/api/v1/users/gone%40x.com", tok, nil); rec.Code != http.StatusOK {
		t.Fatalf("delete: status %d, want 200", rec.Code)
	}
	if rec, _ := e.do("GET", "/api/v1/me", goneTok, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("deleted user's token: status %d, want 401", rec.Code)
	}
}

func TestValidationRules(t *testing.T) {
	e := newTestEnv(t)
	tok := e.login("admin@x.com", "adminpassword")

	cases := []struct {
		name string
		body map[string]string
		want int
	}{
		{"short password", map[string]string{"email": "a@x.com", "password": "short"}, http.StatusBadRequest},
		{"bad email", map[string]string{"email": "notanemail", "password": "longenough1"}, http.StatusBadRequest},
		{"bad role", map[string]string{"email": "b@x.com", "password": "longenough1", "role": "superuser"}, http.StatusBadRequest},
		{"duplicate email", map[string]string{"email": "ADMIN@x.com", "password": "longenough1"}, http.StatusConflict},
		{"valid", map[string]string{"email": "ok@x.com", "password": "longenough1"}, http.StatusCreated},
	}
	for _, c := range cases {
		rec, _ := e.do("POST", "/api/v1/users", tok, c.body)
		if rec.Code != c.want {
			t.Errorf("%s: status %d, want %d (%s)", c.name, rec.Code, c.want, rec.Body.String())
		}
	}

	// Default role when unspecified is the least-privileged one.
	op, err := e.st.GetOperator("ok@x.com")
	if err != nil {
		t.Fatal(err)
	}
	if op.Role != store.RoleOperator {
		t.Errorf("default role = %q, want %q", op.Role, store.RoleOperator)
	}
}

// Changing your own password requires proving the current one.
func TestChangePasswordRequiresCurrent(t *testing.T) {
	e := newTestEnv(t)
	tok := e.login("admin@x.com", "adminpassword")

	if rec, _ := e.do("POST", "/api/v1/me/password", tok, map[string]string{
		"current_password": "wrong", "new_password": "brandnewpw1",
	}); rec.Code != http.StatusUnauthorized {
		t.Errorf("wrong current password: status %d, want 401", rec.Code)
	}
	if rec, _ := e.do("POST", "/api/v1/me/password", tok, map[string]string{
		"current_password": "adminpassword", "new_password": "brandnewpw1",
	}); rec.Code != http.StatusOK {
		t.Errorf("correct current password: status %d, want 200", rec.Code)
	}
	e.login("admin@x.com", "brandnewpw1") // fails the test if the change didn't stick
}

// Renaming your own account must return a token valid for the new address.
func TestSelfRenameReturnsUsableToken(t *testing.T) {
	e := newTestEnv(t)
	tok := e.login("admin@x.com", "adminpassword")

	rec, body := e.do("PUT", "/api/v1/me", tok, map[string]string{"email": "newname@x.com", "name": "Renamed"})
	if rec.Code != http.StatusOK {
		t.Fatalf("rename: status %d (%s)", rec.Code, rec.Body.String())
	}
	newTok, _ := body["token"].(string)
	if newTok == "" {
		t.Fatal("rename returned no replacement token — the session would break")
	}
	if rec, _ := e.do("GET", "/api/v1/me", newTok, nil); rec.Code != http.StatusOK {
		t.Errorf("replacement token: status %d, want 200", rec.Code)
	}
	if rec, _ := e.do("GET", "/api/v1/me", tok, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("stale token after rename: status %d, want 401", rec.Code)
	}
}
