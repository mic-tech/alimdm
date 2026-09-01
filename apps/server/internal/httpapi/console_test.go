package httpapi

// Serving the console.
//
// The console moved from /console/ to the root, and each of its pages now has
// its own URL. Both halves are server behaviour: the browser asks this server
// for /devices before any JavaScript exists to route it.

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ali-mdm/server/internal/auth"
	"ali-mdm/server/internal/blob"
	"ali-mdm/server/internal/store"
)

// newConsoleEnv is newTestEnv with a console directory on disk, which the
// shared one deliberately leaves empty.
func newConsoleEnv(t *testing.T) (*http.ServeMux, string) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	hash, _ := auth.HashPassword("adminpassword")
	st.UpsertOperator(&store.Operator{
		Email: "admin@x.com", Name: "Admin", Role: store.RoleAdmin,
		PasswordHash: hash, CreatedAt: time.Now().UTC().Format(time.RFC3339),
	})

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!doctype html><title>Ali MDM Console</title>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "assets", "index-abc123.js"), []byte("console.log(1)"), 0o644); err != nil {
		t.Fatal(err)
	}

	files := blob.NewStore(filepath.Join(t.TempDir(), "files"))
	srv := New(st, auth.NewSigner("test-secret"), nil, nil, files, NewPokeQueue(), "enroll", "http://x", dir, "")
	return srv.Routes(), dir
}

func get(t *testing.T, mux *http.ServeMux, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
	return rec
}

// The console answers at the root, not at /console/.
func TestConsoleIsServedAtTheRoot(t *testing.T) {
	mux, _ := newConsoleEnv(t)
	rec := get(t, mux, "/")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /: status %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Ali MDM Console") {
		t.Errorf("GET / did not serve the console: %q", rec.Body.String())
	}
}

// The whole point of per-page URLs: someone opens /devices directly — from a
// bookmark, a reload, or a link — and must get the console, not a 404.
func TestEveryPageURLServesTheConsole(t *testing.T) {
	mux, _ := newConsoleEnv(t)
	for _, p := range []string{"/devices", "/groups", "/activity", "/enroll",
		"/packages", "/files", "/app-update", "/profile", "/users"} {
		rec := get(t, mux, p)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s: status %d, want 200 — a reload on that page would 404", p, rec.Code)
			continue
		}
		if !strings.Contains(rec.Body.String(), "Ali MDM Console") {
			t.Errorf("GET %s did not serve the console entry", p)
		}
	}
}

// index.html keeps its URL across deploys while its contents change, so a
// cached copy would go on asking for asset files that no longer exist.
func TestTheEntryPageIsNotCached(t *testing.T) {
	mux, _ := newConsoleEnv(t)
	if cc := get(t, mux, "/devices").Header().Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
}

// Real files still win over the fallback.
func TestAssetsAreServedAndCacheable(t *testing.T) {
	mux, _ := newConsoleEnv(t)
	rec := get(t, mux, "/assets/index-abc123.js")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "console.log") {
		t.Fatalf("asset: status %d body %q", rec.Code, rec.Body.String())
	}
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("Cache-Control = %q; hashed assets should be cacheable", cc)
	}
}

// The console now sits on "/", which catches everything no route claimed. An
// unknown API path must still say so: handing back index.html would give a
// client an HTML page to parse as JSON.
func TestUnknownAPIPathsAreNotTheConsole(t *testing.T) {
	mux, _ := newConsoleEnv(t)
	rec := get(t, mux, "/api/v1/nonexistent")
	if rec.Code != http.StatusNotFound {
		t.Errorf("GET /api/v1/nonexistent: status %d, want 404", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "<!doctype") {
		t.Errorf("an unknown API path returned the console page: %q", rec.Body.String())
	}
}

// A path climbing out of the console directory must not reach the filesystem.
func TestConsolePathsCannotEscape(t *testing.T) {
	mux, _ := newConsoleEnv(t)
	rec := get(t, mux, "/assets/../../etc/passwd")
	if strings.Contains(rec.Body.String(), "root:") {
		t.Fatal("served a file from outside the console directory")
	}
}

// API routes must keep working now that a catch-all sits alongside them.
func TestAPIRoutesStillWinOverTheConsole(t *testing.T) {
	mux, _ := newConsoleEnv(t)
	if rec := get(t, mux, "/healthz"); rec.Body.String() != "ok" {
		t.Errorf("/healthz returned %q", rec.Body.String())
	}
	rec := get(t, mux, "/api/v1/devices")
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("GET /api/v1/devices unauthenticated: status %d, want 401", rec.Code)
	}
}

// A missing asset is a missing file, not a page. Serving index.html there gave
// the browser HTML where it asked for a script, and the parse error it reported
// named neither the file nor the deploy that lost it.
func TestMissingAssetsAre404(t *testing.T) {
	mux, _ := newConsoleEnv(t)
	rec := get(t, mux, "/assets/index-gone.js")
	if rec.Code != http.StatusNotFound {
		t.Errorf("GET a missing asset: status %d, want 404", rec.Code)
	}
}
