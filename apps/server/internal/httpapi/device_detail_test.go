package httpapi

// One device's own page: its record, and its history.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"ali-mdm/server/internal/apk"
	"ali-mdm/server/internal/auth"
	"ali-mdm/server/internal/blob"
	"ali-mdm/server/internal/store"
)

func TestDeviceDetailDescribesOneDevice(t *testing.T) {
	e := newTestEnv(t)
	tok := e.login("admin@x.com", "adminpassword")
	if err := e.st.UpsertGroup(&testGroup); err != nil {
		t.Fatal(err)
	}
	e.newDeviceKey(t, "tablet-1")

	rec, body := e.do("GET", "/api/v1/devices/tablet-1", tok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", rec.Code)
	}
	// The page shows things the list has no room for; without them it would be
	// the same row with more whitespace.
	for _, k := range []string{"id", "label", "group_name", "enrolled_at", "config_version", "pending_commands"} {
		if _, ok := body[k]; !ok {
			t.Errorf("detail has no %q", k)
		}
	}
	if body["id"] != "tablet-1" {
		t.Errorf("id = %v", body["id"])
	}
}

func TestDeviceDetailIsNotFoundForAStranger(t *testing.T) {
	e := newTestEnv(t)
	tok := e.login("admin@x.com", "adminpassword")
	if rec, _ := e.do("GET", "/api/v1/devices/no-such-device", tok, nil); rec.Code != http.StatusNotFound {
		t.Errorf("status %d, want 404", rec.Code)
	}
}

// The device protocol lives under the same prefix, so the new operator route
// must not have swallowed it.
func TestDeviceDetailDoesNotShadowTheDeviceProtocol(t *testing.T) {
	e := newTestEnv(t)
	if err := e.st.UpsertGroup(&testGroup); err != nil {
		t.Fatal(err)
	}
	key := e.newDeviceKey(t, "tablet-1")
	if rec, _ := e.do("POST", "/api/v1/devices/tablet-1/heartbeat", key, map[string]any{}); rec.Code != http.StatusOK {
		t.Errorf("heartbeat: status %d, want 200 — the device protocol is broken", rec.Code)
	}
}

// A device key must not read a device's record: it is an operator view, and a
// stolen tablet key would otherwise report on the fleet.
func TestDeviceDetailRefusesADeviceKey(t *testing.T) {
	e := newTestEnv(t)
	key := e.newDeviceKey(t, "tablet-1")
	if rec, _ := e.do("GET", "/api/v1/devices/tablet-1", key, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("status %d, want 401", rec.Code)
	}
}

// The history is the point of the page: the same feed, narrowed to one device.
func TestEventsCanBeNarrowedToOneDevice(t *testing.T) {
	e := newTestEnv(t)
	tok := e.login("admin@x.com", "adminpassword")
	for _, ev := range []store.Event{
		{Kind: "command_sent", Severity: store.EventInfo, Actor: "a@x.com", DeviceID: "tablet-1", Summary: "one"},
		{Kind: "command_sent", Severity: store.EventInfo, Actor: "a@x.com", DeviceID: "tablet-2", Summary: "two"},
		{Kind: "apk_uploaded", Severity: store.EventInfo, Actor: "a@x.com", DeviceID: "", Summary: "fleet"},
		{Kind: "device_offline", Severity: store.EventWarn, Actor: "system", DeviceID: "tablet-1", Summary: "three"},
	} {
		ev.At = time.Now().UTC().Format(time.RFC3339)
		if err := e.st.RecordEvent(&ev); err != nil {
			t.Fatal(err)
		}
	}

	_, body := e.do("GET", "/api/v1/events?device=tablet-1", tok, nil)
	events, _ := body["events"].([]any)
	if len(events) != 2 {
		t.Fatalf("got %d events for tablet-1, want 2 (its own two, not the fleet's)", len(events))
	}
	for _, raw := range events {
		ev := raw.(map[string]any)
		if ev["device_id"] != "tablet-1" {
			t.Errorf("another device's entry leaked in: %v", ev["summary"])
		}
	}
	// "Showing 2 of 4" on a device page would be counting a fleet it is not
	// displaying.
	if total, ok := body["total"].(float64); !ok || int(total) != 2 {
		t.Errorf("total = %v, want 2 — the count must follow the same filter as the list", body["total"])
	}
}

// Paging must stay inside the filter, or "load older" would pull in the fleet.
func TestDeviceHistoryPagesWithinTheDevice(t *testing.T) {
	e := newTestEnv(t)
	tok := e.login("admin@x.com", "adminpassword")
	for i := 0; i < 6; i++ {
		dev := "tablet-1"
		if i%2 == 1 {
			dev = "tablet-2"
		}
		if err := e.st.RecordEvent(&store.Event{
			Kind: "command_sent", Severity: store.EventInfo, Actor: "a@x.com",
			DeviceID: dev, Summary: "entry", At: time.Now().UTC().Format(time.RFC3339),
		}); err != nil {
			t.Fatal(err)
		}
	}
	_, first := e.do("GET", "/api/v1/events?device=tablet-1&limit=2", tok, nil)
	events, _ := first["events"].([]any)
	if len(events) != 2 || first["has_more"] != true {
		t.Fatalf("first page: %d events, has_more %v", len(events), first["has_more"])
	}
	last := events[len(events)-1].(map[string]any)["id"].(float64)

	_, older := e.do("GET", "/api/v1/events?device=tablet-1&limit=5&before="+strconv.Itoa(int(last)), tok, nil)
	rest, _ := older["events"].([]any)
	if len(rest) != 1 {
		t.Fatalf("older page: %d events, want the 1 remaining for this device", len(rest))
	}
	for _, raw := range rest {
		if raw.(map[string]any)["device_id"] != "tablet-1" {
			t.Errorf("paging escaped the device filter: %v", raw)
		}
	}
}

// An install aimed at one device should be findable in that device's history.
// Attributed to no device, it was only ever visible in the fleet feed.
func TestASingleDeviceInstallLandsInThatDevicesHistory(t *testing.T) {
	if got := singleTarget([]string{"tablet-1"}); got != "tablet-1" {
		t.Errorf("singleTarget one device = %q, want tablet-1", got)
	}
	// Two devices is not one device's history; twelve rows saying the same
	// thing would bury the feed.
	if got := singleTarget([]string{"tablet-1", "tablet-2"}); got != "" {
		t.Errorf("singleTarget two devices = %q, want empty", got)
	}
	if got := singleTarget(nil); got != "" {
		t.Errorf("singleTarget fleet-wide = %q, want empty", got)
	}
}

// The command list is read by the device page, so it has to speak the same
// snake_case as the rest of this API. Encoding the store struct directly gave
// Go field names, and the page showed a row of empty cells for every command.
func TestCommandListSpeaksSnakeCase(t *testing.T) {
	e := newTestEnv(t)
	tok := e.login("admin@x.com", "adminpassword")
	if err := e.st.UpsertGroup(&testGroup); err != nil {
		t.Fatal(err)
	}
	e.newDeviceKey(t, "tablet-1")
	if rec, _ := e.do("POST", "/api/v1/devices/tablet-1/commands", tok, map[string]any{"type": "reboot"}); rec.Code != http.StatusOK {
		t.Fatalf("enqueue: status %d", rec.Code)
	}

	rec, _ := e.do("GET", "/api/v1/devices/tablet-1/commands", tok, nil)
	var cmds []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &cmds); err != nil {
		t.Fatalf("decode: %v — body %s", err, rec.Body.String())
	}
	if len(cmds) != 1 {
		t.Fatalf("got %d commands, want 1", len(cmds))
	}
	for _, k := range []string{"id", "type", "status", "created_at", "err_msg"} {
		if _, ok := cmds[0][k]; !ok {
			t.Errorf("command has no %q: %v", k, cmds[0])
		}
	}
	if cmds[0]["type"] != "reboot" || cmds[0]["status"] != "pending" {
		t.Errorf("command reads %v", cmds[0])
	}
}

// A command an operator sends must run once, with the parameters they gave.
//
// The heartbeat used to turn the accompanying wake poke back into a second
// command, so every console command reached the device twice: once correctly,
// and once with empty parameters. On the fleet that was a "launch_app success"
// next to a "launch_app error: Missing package name" for every launch.
func TestACommandIsQueuedOnceWithItsParameters(t *testing.T) {
	e := newTestEnv(t)
	tok := e.login("admin@x.com", "adminpassword")
	if err := e.st.UpsertGroup(&testGroup); err != nil {
		t.Fatal(err)
	}
	key := e.newDeviceKey(t, "tablet-1")

	if rec, _ := e.do("POST", "/api/v1/devices/tablet-1/commands", tok,
		map[string]any{"type": "launch_app", "params": map[string]any{"package_name": "com.example"}}); rec.Code != http.StatusOK {
		t.Fatalf("enqueue: status %d", rec.Code)
	}
	// Two heartbeats: the poke is drained on the first, and a second would have
	// shown any repeat.
	e.do("POST", "/api/v1/devices/tablet-1/heartbeat", key, map[string]any{})
	e.do("POST", "/api/v1/devices/tablet-1/heartbeat", key, map[string]any{})

	cmds, err := e.st.ListCommands("tablet-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(cmds) != 1 {
		for _, c := range cmds {
			t.Logf("  %s %s params=%s", c.ID, c.Type, c.Params)
		}
		t.Fatalf("got %d commands, want 1 — the device would act on it more than once", len(cmds))
	}
	if !strings.Contains(cmds[0].Params, "com.example") {
		t.Errorf("params = %s, want the package the operator sent", cmds[0].Params)
	}
}

// A wake-only poke must not become a command at all: the app has no handler for
// these, so the row could only ever settle as "Unsupported command type".
func TestWakeOnlyPokesDoNotBecomeCommands(t *testing.T) {
	e := newTestEnv(t)
	tok := e.login("admin@x.com", "adminpassword")
	if err := e.st.UpsertGroup(&testGroup); err != nil {
		t.Fatal(err)
	}
	key := e.newDeviceKey(t, "tablet-1")

	if rec, _ := e.do("POST", "/api/v1/devices/tablet-1/stream/start", tok, nil); rec.Code != http.StatusOK {
		t.Fatalf("start stream: status %d", rec.Code)
	}
	e.do("POST", "/api/v1/devices/tablet-1/heartbeat", key, map[string]any{})

	cmds, _ := e.st.ListCommands("tablet-1")
	for _, c := range cmds {
		if c.Type == "start_stream" {
			t.Fatalf("a start_stream command was queued; live view is carried by the heartbeat's own flag")
		}
	}
}

// A tablet enrolled by QR installs the build staged on the App update page, and
// nothing else. There used to be a second source — a file path in an env var —
// which is how a newly provisioned tablet arrived four days and nine versions
// behind the fleet with nothing on screen to say so.
func TestQRProvisioningServesOnlyTheStagedRelease(t *testing.T) {
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

	agentRoot := t.TempDir()
	files := blob.NewStore(filepath.Join(t.TempDir(), "files"))
	srv := New(st, auth.NewSigner("test-secret"), nil, apk.NewStore(agentRoot), files,
		NewPokeQueue(), "enroll", "http://x", "")
	mux := srv.Routes()
	tok := (&testEnv{t: t, mux: mux, st: st, srv: srv}).login("admin@x.com", "adminpassword")

	// Nothing staged: refuse, and say so on the page where the QR is printed
	// rather than letting a tablet find out mid-provisioning.
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/provision/apk", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("with no staged build: status %d, want 404", rec.Code)
	}
	req := httptest.NewRequest("GET", "/api/v1/provision/qr", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	qr := httptest.NewRecorder()
	mux.ServeHTTP(qr, req)
	var info map[string]any
	json.Unmarshal(qr.Body.Bytes(), &info)
	if info["ready"] != false {
		t.Errorf("ready = %v with no staged build, want false", info["ready"])
	}
	if !strings.Contains(qr.Body.String(), "App update") {
		t.Errorf("the problem does not say where to upload a build: %s", qr.Body.String())
	}

	// Stage one, as the App update page does.
	if err := os.WriteFile(filepath.Join(agentRoot, "app-release.apk"), []byte("the current build"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveAgentRelease(&store.AgentRelease{
		VersionCode: 65, VersionName: "1.2.34", FileName: "app-release.apk",
		SHA256: "x", Size: 17, CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		t.Fatal(err)
	}

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/provision/apk", nil))
	if got := rec.Body.String(); got != "the current build" {
		t.Errorf("served %q, want the staged release", got)
	}
	req = httptest.NewRequest("GET", "/api/v1/provision/qr", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	qr = httptest.NewRecorder()
	mux.ServeHTTP(qr, req)
	json.Unmarshal(qr.Body.Bytes(), &info)
	if info["build"] != "1.2.34" {
		t.Errorf("build = %v, want the staged version so the page can name it", info["build"])
	}
}

// A device log is a minute-by-minute account of a classroom's tablet. Asking
// for one, and reading it, are both administrator work.
func TestDeviceLogsAreAdminOnly(t *testing.T) {
	e := newTestEnv(t)
	admin := e.login("admin@x.com", "adminpassword")
	if err := e.st.UpsertGroup(&testGroup); err != nil {
		t.Fatal(err)
	}
	e.newDeviceKey(t, "tablet-1")

	// An operator, who can see the fleet but not its logs.
	if rec, _ := e.do("POST", "/api/v1/users", admin, map[string]any{
		"email": "op@x.com", "name": "Op", "role": "operator", "password": "operatorpass",
	}); rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("create operator: status %d", rec.Code)
	}
	opTok := e.login("op@x.com", "operatorpass")

	for _, call := range []struct{ method, path string }{
		{"POST", "/api/v1/devices/tablet-1/logs"},
		{"GET", "/api/v1/devices/tablet-1/logs"},
	} {
		if rec, _ := e.do(call.method, call.path, opTok, nil); rec.Code != http.StatusForbidden {
			t.Errorf("%s %s as operator: status %d, want 403", call.method, call.path, rec.Code)
		}
	}
	if rec, _ := e.do("POST", "/api/v1/devices/tablet-1/logs", admin, nil); rec.Code != http.StatusOK {
		t.Errorf("POST as admin: status %d, want 200", rec.Code)
	}
}

// The round trip: the console asks, the tablet answers on its next check-in,
// and the answer comes back as the log.
func TestADeviceAnswersWithItsLog(t *testing.T) {
	e := newTestEnv(t)
	tok := e.login("admin@x.com", "adminpassword")
	if err := e.st.UpsertGroup(&testGroup); err != nil {
		t.Fatal(err)
	}
	key := e.newDeviceKey(t, "tablet-1")

	// Nothing asked yet.
	_, body := e.do("GET", "/api/v1/devices/tablet-1/logs", tok, nil)
	if body["status"] != "none" {
		t.Errorf("before asking: status = %v, want none", body["status"])
	}

	e.do("POST", "/api/v1/devices/tablet-1/logs", tok, nil)
	_, body = e.do("GET", "/api/v1/devices/tablet-1/logs", tok, nil)
	if body["status"] != "pending" {
		t.Errorf("after asking: status = %v, want pending", body["status"])
	}

	// The device collects the commands and reports back, as the agent does.
	_, cmds := e.do("GET", "/api/v1/devices/tablet-1/commands/", key, nil)
	list, _ := cmds["commands"].([]any)
	if len(list) != 1 {
		t.Fatalf("device sees %d commands, want 1", len(list))
	}
	cmdID := list[0].(map[string]any)["id"].(string)
	if rec, _ := e.do("POST", "/api/v1/commands/"+cmdID+"/result/", key, map[string]any{
		"status": "success",
		"result": map[string]any{
			"app_log":      "09-01 22:10:00 D/KioskScreen: launched com.google.android.calculator",
			"logcat":       "I/OverlayService: Bringing AliMDM to foreground",
			"collected_at": "2026-09-01T22:10:01Z",
			"state":        map[string]any{"usage_access": false, "accessibility_running": false},
		},
	}); rec.Code != http.StatusOK {
		t.Fatalf("report result: status %d", rec.Code)
	}

	_, body = e.do("GET", "/api/v1/devices/tablet-1/logs", tok, nil)
	if body["status"] != "success" {
		t.Fatalf("after the answer: status = %v", body["status"])
	}
	if s, _ := body["app_log"].(string); !strings.Contains(s, "calculator") {
		t.Errorf("app_log = %q", s)
	}
	state, _ := body["state"].(map[string]any)
	if state == nil || state["usage_access"] != false {
		t.Errorf("state did not come back: %v", body["state"])
	}
}
