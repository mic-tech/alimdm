package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// reportApps posts an inventory the way a tablet does.
func (e *testEnv) reportApps(t *testing.T, devKey string, deviceID string, entries []map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]any{"entries": entries})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/api/v1/devices/"+deviceID+"/apps", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+devKey)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req)
	return rec
}

// The round trip the tab depends on: ask, the device answers, the console reads
// it back with the time it was taken.
func TestAnInventoryIsStoredAndServedBack(t *testing.T) {
	e := newTestEnv(t)
	tok := e.login("admin@x.com", "adminpassword")
	devKey := e.newDeviceKey(t, "tablet-1")

	// Nothing yet: never checked, rather than "no apps installed".
	rec, body := e.do("GET", "/api/v1/devices/tablet-1/apps", tok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if body["fetched_at"] != "" {
		t.Fatalf("fetched_at = %v before the device has ever answered", body["fetched_at"])
	}

	if rec, _ := e.do("POST", "/api/v1/devices/tablet-1/apps/refresh", tok, map[string]any{}); rec.Code != http.StatusOK {
		t.Fatalf("refresh: status %d (%s)", rec.Code, rec.Body.String())
	}
	// The request reaches the device as a command it knows.
	cmds, err := e.st.ClaimPendingCommands("tablet-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(cmds) != 1 || cmds[0].Type != "list_apps" {
		t.Fatalf("queued %+v, want one list_apps", cmds)
	}

	if rec := e.reportApps(t, devKey, "tablet-1", []map[string]any{
		{"package_name": "com.example.app", "label": "Example", "system": false, "version_name": "2.1"},
		{"package_name": "com.android.settings", "label": "Settings", "system": true},
	}); rec.Code != http.StatusOK {
		t.Fatalf("report: status %d (%s)", rec.Code, rec.Body.String())
	}

	rec, body = e.do("GET", "/api/v1/devices/tablet-1/apps", tok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	entries, _ := body["entries"].([]any)
	if len(entries) != 2 {
		t.Fatalf("%d entries, want 2", len(entries))
	}
	if body["fetched_at"] == "" {
		t.Fatal("fetched_at is empty after the device answered — the console would say 'never checked'")
	}
	first, _ := entries[0].(map[string]any)
	if first["package_name"] != "com.example.app" || first["version_name"] != "2.1" {
		t.Fatalf("entry did not survive the round trip: %v", first)
	}
}

// The guardrail that matters. With the agent gone from a Device Owner tablet
// nothing is left that could enroll it again, so the tablet has to be factory
// reset by hand. This must be refused at the server, not only in a dialog.
func TestAliMdmCannotBeUninstalledFromTheConsole(t *testing.T) {
	e := newTestEnv(t)
	tok := e.login("admin@x.com", "adminpassword")
	e.newDeviceKey(t, "tablet-1")

	rec, body := e.do("DELETE", "/api/v1/devices/tablet-1/apps/"+url.PathEscape(agentPackageName), tok, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400 — the console must not be able to strand a tablet", rec.Code)
	}
	if body["error"] == nil {
		t.Fatal("no reason given")
	}
	// And nothing was queued for the device to act on.
	cmds, _ := e.st.ClaimPendingCommands("tablet-1")
	for _, c := range cmds {
		if c.Type == "uninstall_app" {
			t.Fatal("an uninstall of Ali MDM was queued anyway")
		}
	}
}

// Any other package is queued as a command naming it, so the device knows what
// to remove.
func TestUninstallQueuesACommandNamingThePackage(t *testing.T) {
	e := newTestEnv(t)
	tok := e.login("admin@x.com", "adminpassword")
	e.newDeviceKey(t, "tablet-1")

	if rec, _ := e.do("DELETE", "/api/v1/devices/tablet-1/apps/com.example.app", tok, nil); rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	cmds, err := e.st.ClaimPendingCommands("tablet-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(cmds) != 1 || cmds[0].Type != "uninstall_app" {
		t.Fatalf("queued %+v, want one uninstall_app", cmds)
	}
	var params map[string]any
	json.Unmarshal([]byte(cmds[0].Params), &params)
	if params["package"] != "com.example.app" {
		t.Fatalf("command params %v, want the package name", params)
	}
}

// A device may only report its own inventory.
func TestOneDeviceCannotReportAnothersApps(t *testing.T) {
	e := newTestEnv(t)
	tok := e.login("admin@x.com", "adminpassword")
	keyOne := e.newDeviceKey(t, "tablet-1")
	e.newDeviceKey(t, "tablet-2")

	// tablet-1's key, tablet-2's path.
	e.reportApps(t, keyOne, "tablet-2", []map[string]any{
		{"package_name": "com.example.planted", "label": "Planted"},
	})
	_, body := e.do("GET", "/api/v1/devices/tablet-2/apps", tok, nil)
	entries, _ := body["entries"].([]any)
	if len(entries) != 0 {
		t.Fatalf("tablet-2 shows %d apps reported by tablet-1", len(entries))
	}
}

// One device must not be able to push an unbounded blob into the row that is
// read back whole on every view.
func TestAnOversizedInventoryIsTrimmed(t *testing.T) {
	e := newTestEnv(t)
	tok := e.login("admin@x.com", "adminpassword")
	devKey := e.newDeviceKey(t, "tablet-1")

	many := make([]map[string]any, maxReportedApps+50)
	for i := range many {
		many[i] = map[string]any{"package_name": "com.example.app", "label": "App"}
	}
	if rec := e.reportApps(t, devKey, "tablet-1", many); rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	_, body := e.do("GET", "/api/v1/devices/tablet-1/apps", tok, nil)
	entries, _ := body["entries"].([]any)
	if len(entries) != maxReportedApps {
		t.Fatalf("stored %d entries, want the cap of %d", len(entries), maxReportedApps)
	}
}

// The console polls, and most polls find nothing new. A large body repeated
// every few seconds for data that changes once a day is the waste worth
// removing — not the request itself, which is what keeps the console simple.
func TestUnchangedPollsAnswerWithoutABody(t *testing.T) {
	e := newTestEnv(t)
	tok := e.login("admin@x.com", "adminpassword")
	devKey := e.newDeviceKey(t, "tablet-1")
	e.reportApps(t, devKey, "tablet-1", []map[string]any{
		{"package_name": "com.example.app", "label": "Example"},
	})

	first, _ := e.do("GET", "/api/v1/devices/tablet-1/apps", tok, nil)
	if first.Code != http.StatusOK {
		t.Fatalf("status %d", first.Code)
	}
	etag := first.Header().Get("ETag")
	if etag == "" {
		t.Fatal("no ETag — every poll would resend the whole inventory")
	}
	// no-store would forbid keeping a copy at all; no-cache keeps one and
	// revalidates, which is what makes the 304 below useful.
	if cc := first.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Fatalf("Cache-Control %q, want no-cache", cc)
	}

	req := httptest.NewRequest("GET", "/api/v1/devices/tablet-1/apps", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("If-None-Match", etag)
	again := httptest.NewRecorder()
	e.mux.ServeHTTP(again, req)
	if again.Code != http.StatusNotModified {
		t.Fatalf("status %d for an unchanged poll, want 304", again.Code)
	}
	if again.Body.Len() != 0 {
		t.Fatalf("304 carried %d bytes", again.Body.Len())
	}
}

// A stale validator must not suppress real changes.
func TestAChangedInventoryIsSentAgain(t *testing.T) {
	e := newTestEnv(t)
	tok := e.login("admin@x.com", "adminpassword")
	devKey := e.newDeviceKey(t, "tablet-1")
	e.reportApps(t, devKey, "tablet-1", []map[string]any{{"package_name": "com.example.one"}})

	first, _ := e.do("GET", "/api/v1/devices/tablet-1/apps", tok, nil)
	etag := first.Header().Get("ETag")

	e.reportApps(t, devKey, "tablet-1", []map[string]any{
		{"package_name": "com.example.one"}, {"package_name": "com.example.two"},
	})

	req := httptest.NewRequest("GET", "/api/v1/devices/tablet-1/apps", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("If-None-Match", etag)
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d after the inventory changed, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "com.example.two") {
		t.Fatal("the new package is missing from the response")
	}
	if rec.Header().Get("ETag") == etag {
		t.Fatal("the ETag did not change with the body")
	}
}

// Only the console's read-only lists may carry validators. The device-facing
// endpoints hand work over and mark it claimed in the same request, so a 304
// would record installs as sent and deliver nothing.
func TestDeviceFacingEndpointsAreNeverCached(t *testing.T) {
	e := newTestEnv(t)
	devKey := e.newDeviceKey(t, "tablet-1")

	for _, path := range []string{
		"/api/v1/devices/tablet-1/updates",
		"/api/v1/devices/tablet-1/commands",
		"/api/v1/devices/tablet-1/files",
	} {
		req := httptest.NewRequest("GET", path, nil)
		req.Header.Set("Authorization", "Bearer "+devKey)
		rec := httptest.NewRecorder()
		e.mux.ServeHTTP(rec, req)
		if tag := rec.Header().Get("ETag"); tag != "" {
			t.Errorf("%s carries an ETag (%s) — a 304 here would claim work and never deliver it", path, tag)
		}
	}
}
