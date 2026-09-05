package httpapi

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"ali-mdm/server/internal/apk"
	"ali-mdm/server/internal/auth"
	"ali-mdm/server/internal/blob"
	"ali-mdm/server/internal/store"
)

// newAPKTestEnv is newTestEnv with a real package store, which the split tests
// need because they move actual APKs around on disk.
func newAPKTestEnv(t *testing.T) *testEnv {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	hash, _ := auth.HashPassword("adminpassword")
	if err := st.UpsertOperator(&store.Operator{
		Email: "admin@x.com", Name: "Admin", Role: store.RoleAdmin,
		PasswordHash: hash, CreatedAt: nowRFC3339(),
	}); err != nil {
		t.Fatal(err)
	}
	apks := apk.NewStore(filepath.Join(t.TempDir(), "apks"))
	files := blob.NewStore(filepath.Join(t.TempDir(), "files"))
	srv := New(st, auth.NewSigner("test-secret"), apks, apks, files, NewPokeQueue(),
		"enroll", "https://mdm.example.test", "")
	return &testEnv{t: t, mux: srv.Routes(), st: st, srv: srv}
}

// splitArchive builds a .xapk holding a base APK and the given splits. The base
// is a real (minimal) APK so the manifest parser can read a package name out of
// it, which is what the stored name comes from.
func splitArchive(t *testing.T, pkg string, splits map[string]string) []byte {
	t.Helper()
	var out bytes.Buffer
	zw := zip.NewWriter(&out)

	w, err := zw.Create("base.apk")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(miniAPK(t, pkg)); err != nil {
		t.Fatal(err)
	}
	for name, body := range splits {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// uploadPackage posts a package file the way the console does.
func (e *testEnv) uploadPackage(t *testing.T, tok, name string, body []byte) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(body); err != nil {
		t.Fatal(err)
	}
	mw.Close()

	req := httptest.NewRequest("POST", "/api/v1/apks", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req)
	out := map[string]any{}
	json.Unmarshal(rec.Body.Bytes(), &out)
	return rec, out
}

// The point of the feature: an archive of several APKs becomes one installable
// package, named after what is actually inside it.
func TestUploadingASplitArchiveStoresItUnderItsPackageName(t *testing.T) {
	e := newAPKTestEnv(t)
	tok := e.login("admin@x.com", "adminpassword")

	rec, body := e.uploadPackage(t, tok, "SomeApp_v3.xapk", splitArchive(t, "com.example.app", map[string]string{
		"split_0.apk": "arm64 libraries",
		"split_1.apk": "xxhdpi drawables",
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("upload: status %d (%s)", rec.Code, rec.Body.String())
	}
	// Named for the package, not the file: an operator should not have to know
	// that the enrollment match wants "com.example.app" in the name.
	if body["name"] != "com.example.app.xapk" {
		t.Fatalf("stored as %v, want com.example.app.xapk", body["name"])
	}
	parts, _ := body["parts"].([]any)
	if len(parts) != 3 {
		t.Fatalf("parts = %v, want base plus two splits", body["parts"])
	}
	if parts[0] != "base.apk" {
		t.Fatalf("parts[0] = %v, want the base first", parts[0])
	}
}

// The whole set reaches the device, base first, and every part downloads.
func TestADeviceIsOfferedEveryPartOfASplitPackage(t *testing.T) {
	e := newAPKTestEnv(t)
	tok := e.login("admin@x.com", "adminpassword")
	devKey := e.newDeviceKey(t, "tablet-1")

	if rec, _ := e.uploadPackage(t, tok, "app.xapk", splitArchive(t, "com.example.app", map[string]string{
		"split_0.apk": "arm64 libraries",
		"split_1.apk": "xxhdpi drawables",
	})); rec.Code != http.StatusOK {
		t.Fatalf("upload: status %d (%s)", rec.Code, rec.Body.String())
	}
	if rec, _ := e.do("POST", "/api/v1/apks/com.example.app.xapk/install", tok,
		map[string]any{"package_name": "com.example.app", "devices": []string{"tablet-1"}}); rec.Code != http.StatusOK {
		t.Fatalf("install: status %d (%s)", rec.Code, rec.Body.String())
	}

	asDevice := func(path string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest("GET", path, nil)
		req.Header.Set("Authorization", "Bearer "+devKey)
		rec := httptest.NewRecorder()
		e.mux.ServeHTTP(rec, req)
		return rec
	}
	var offers []map[string]any
	json.Unmarshal(asDevice("/api/v1/devices/tablet-1/updates").Body.Bytes(), &offers)
	if len(offers) != 1 {
		t.Fatalf("device was offered %d updates, want 1", len(offers))
	}
	urls, _ := offers[0]["split_urls"].([]any)
	if len(urls) != 3 {
		t.Fatalf("split_urls = %v, want all three parts — a split package installs whole or not at all", offers[0]["split_urls"])
	}
	if !strings.HasSuffix(urls[0].(string), "/base.apk") {
		t.Fatalf("split_urls[0] = %v, want the base first", urls[0])
	}
	// download_url points at the base, so a build that predates split support
	// fails on the missing split rather than installing a broken app.
	if offers[0]["download_url"] != urls[0] {
		t.Fatalf("download_url = %v, want the base URL %v", offers[0]["download_url"], urls[0])
	}

	for _, u := range urls {
		path := strings.TrimPrefix(u.(string), "https://mdm.example.test")
		rec := asDevice(path)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: status %d", path, rec.Code)
		}
		if rec.Body.Len() == 0 {
			t.Fatalf("GET %s served nothing", path)
		}
	}
}

// A part name is device-supplied input on an authenticated route. It must not
// be usable to read anything but a part of that package.
func TestAPartNameCannotReachOutsideItsPackage(t *testing.T) {
	e := newAPKTestEnv(t)
	tok := e.login("admin@x.com", "adminpassword")
	devKey := e.newDeviceKey(t, "tablet-1")
	e.uploadPackage(t, tok, "app.xapk", splitArchive(t, "com.example.app", map[string]string{
		"split_0.apk": "arm64",
	}))

	for _, path := range []string{
		"/api/v1/apk/com.example.app.xapk/..%2F..%2Fetc%2Fpasswd",
		"/api/v1/apk/com.example.app.xapk/nothing.apk",
		"/api/v1/apk/com.example.app.xapk/base.txt",
	} {
		req := httptest.NewRequest("GET", path, nil)
		req.Header.Set("Authorization", "Bearer "+devKey)
		rec := httptest.NewRecorder()
		e.mux.ServeHTTP(rec, req)
		if rec.Code == http.StatusOK {
			t.Fatalf("GET %s returned 200", path)
		}
	}
}

// A plain APK keeps its old shape exactly: one download_url, no split_urls, and
// a real sha256 in the listing.
func TestAPlainAPKIsUnchangedBySplitSupport(t *testing.T) {
	e := newAPKTestEnv(t)
	tok := e.login("admin@x.com", "adminpassword")
	devKey := e.newDeviceKey(t, "tablet-1")

	// Over the store's 50KB floor.
	body := append(miniAPK(t, "com.example.plain"), make([]byte, 60*1024)...)
	if rec, _ := e.uploadPackage(t, tok, "plain.apk", body); rec.Code != http.StatusOK {
		t.Fatalf("upload: status %d (%s)", rec.Code, rec.Body.String())
	}
	if rec, _ := e.do("POST", "/api/v1/apks/plain.apk/install", tok,
		map[string]any{"package_name": "com.example.plain", "devices": []string{"tablet-1"}}); rec.Code != http.StatusOK {
		t.Fatalf("install: status %d (%s)", rec.Code, rec.Body.String())
	}

	req := httptest.NewRequest("GET", "/api/v1/devices/tablet-1/updates", nil)
	req.Header.Set("Authorization", "Bearer "+devKey)
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req)
	var offers []map[string]any
	json.Unmarshal(rec.Body.Bytes(), &offers)
	if len(offers) != 1 {
		t.Fatalf("%d updates offered, want 1", len(offers))
	}
	if _, ok := offers[0]["split_urls"]; ok {
		t.Fatal("a plain APK was offered as a split package")
	}
	if !strings.HasSuffix(offers[0]["download_url"].(string), "/apk/plain.apk") {
		t.Fatalf("download_url = %v", offers[0]["download_url"])
	}
}

// An archive that cannot be installed must be refused at upload, in front of the
// operator, rather than on every tablet at the next check-in.
func TestABadArchiveIsRefusedAtUpload(t *testing.T) {
	e := newAPKTestEnv(t)
	tok := e.login("admin@x.com", "adminpassword")

	var noAPKs bytes.Buffer
	zw := zip.NewWriter(&noAPKs)
	w, _ := zw.Create("manifest.json")
	w.Write([]byte(`{"package_name":"com.example.app"}`))
	zw.Close()

	for _, tc := range []struct {
		name string
		body []byte
	}{
		{"empty.xapk", noAPKs.Bytes()},
		{"notazip.xapk", []byte("this is not a zip")},
	} {
		rec, _ := e.uploadPackage(t, tok, tc.name, tc.body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: status %d, want 400", tc.name, rec.Code)
		}
	}
	// And nothing was left in the catalogue by the attempts.
	apks, _ := e.st.ListAPKs()
	if len(apks) != 0 {
		t.Fatalf("%d packages in the catalogue after failed uploads", len(apks))
	}
}
