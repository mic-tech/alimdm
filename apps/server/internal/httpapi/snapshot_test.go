package httpapi

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
)

// uploadSnapshot posts a still the way the tablet's screenshot command does.
func (e *testEnv) uploadSnapshot(t *testing.T, id, key string, body []byte) int {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("image", "screenshot.png")
	if err != nil {
		t.Fatal(err)
	}
	part.Write(body)
	mw.Close()

	req := httptest.NewRequest("POST", "/api/v1/devices/"+id+"/screenshot/", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req)
	return rec.Code
}

// The upload used to be parsed and thrown away, so nothing could ever be shown.
func TestSnapshotIsKeptAndServedBack(t *testing.T) {
	e := newTestEnv(t)
	tok := e.login("admin@x.com", "adminpassword")
	key := e.newDeviceKey(t, "tablet-1")

	want := []byte("\x89PNG\r\n\x1a\n fake screen")
	if code := e.uploadSnapshot(t, "tablet-1", key, want); code != http.StatusOK {
		t.Fatalf("upload: status %d", code)
	}

	rec, _ := e.do("GET", "/api/v1/devices/tablet-1/snapshot", tok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("fetch: status %d", rec.Code)
	}
	if !bytes.Equal(rec.Body.Bytes(), want) {
		t.Errorf("served %q, want the uploaded bytes", rec.Body.Bytes())
	}
	if rec.Header().Get("X-Snapshot-At") == "" {
		t.Error("no timestamp header; the console could not label the still's age")
	}
}

// A device with no still yet must be distinguishable from an error.
func TestSnapshotMetaBeforeAnyUpload(t *testing.T) {
	e := newTestEnv(t)
	tok := e.login("admin@x.com", "adminpassword")
	e.newDeviceKey(t, "tablet-1")

	_, body := e.do("GET", "/api/v1/devices/tablet-1/snapshot/meta", tok, nil)
	if body["has_snapshot"] != false {
		t.Errorf("has_snapshot = %v before any upload, want false", body["has_snapshot"])
	}
	rec, _ := e.do("GET", "/api/v1/devices/tablet-1/snapshot", tok, nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("fetch with no snapshot: status %d, want 404", rec.Code)
	}
}

// A still belongs to the device that sent it, not to whatever the path says.
func TestSnapshotIsScopedToItsDevice(t *testing.T) {
	e := newTestEnv(t)
	tok := e.login("admin@x.com", "adminpassword")
	e.newDeviceKey(t, "tablet-1")
	otherKey := e.newDeviceKey(t, "tablet-2")

	// tablet-2's key, tablet-1 in the path.
	e.uploadSnapshot(t, "tablet-1", otherKey, []byte("from tablet-2"))

	rec, _ := e.do("GET", "/api/v1/devices/tablet-1/snapshot", tok, nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("tablet-1 has a snapshot it never sent: status %d", rec.Code)
	}
	rec, _ = e.do("GET", "/api/v1/devices/tablet-2/snapshot", tok, nil)
	if rec.Code != http.StatusOK {
		t.Errorf("tablet-2's own snapshot: status %d, want 200", rec.Code)
	}
}

// Asking for a fresh still has to reach the tablet as a command.
func TestRequestingASnapshotQueuesAScreenshotCommand(t *testing.T) {
	e := newTestEnv(t)
	tok := e.login("admin@x.com", "adminpassword")
	key := e.newDeviceKey(t, "tablet-1")

	if rec, _ := e.do("POST", "/api/v1/devices/tablet-1/snapshot/request", tok, nil); rec.Code != http.StatusOK {
		t.Fatalf("request: status %d", rec.Code)
	}
	for _, c := range e.deviceCommands(t, "tablet-1", key) {
		if c["type"] == "screenshot" {
			return
		}
	}
	t.Error("no screenshot command reached the tablet")
}

// Removing a device must not leave its picture in memory.
func TestRemovingADeviceForgetsItsSnapshot(t *testing.T) {
	e := newTestEnv(t)
	tok := e.login("admin@x.com", "adminpassword")
	key := e.newDeviceKey(t, "tablet-1")
	e.uploadSnapshot(t, "tablet-1", key, []byte("a screen"))

	if rec, _ := e.do("DELETE", "/api/v1/devices/tablet-1", tok, nil); rec.Code != http.StatusOK {
		t.Fatalf("delete: status %d (%s)", rec.Code, rec.Body.String())
	}
	if _, ok := e.srv.snapshots.get("tablet-1"); ok {
		t.Error("the picture of a removed device is still held in memory")
	}
	_ = json.Marshal
}
