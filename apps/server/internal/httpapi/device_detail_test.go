package httpapi

// One device's own page: its record, and its history.

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

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
