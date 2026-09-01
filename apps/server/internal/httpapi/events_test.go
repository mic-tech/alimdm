package httpapi

import (
	"net/http"
	"strconv"
	"testing"
	"time"

	"ali-mdm/server/internal/auth"
	"ali-mdm/server/internal/store"
)

// newDeviceKey enrols a device directly and returns its API key.
func (e *testEnv) newDeviceKey(t *testing.T, id string) string {
	t.Helper()
	key, err := auth.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if err := e.st.CreateDevice(&store.Device{
		ID: id, GroupID: "default", APIKeyHash: auth.HashAPIKey(key),
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		t.Fatal(err)
	}
	return key
}

// feed reads the notification feed as the given operator.
func (e *testEnv) feed(t *testing.T, tok string) ([]any, float64) {
	t.Helper()
	rec, body := e.do("GET", "/api/v1/events", tok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /events: status %d (%s)", rec.Code, rec.Body.String())
	}
	list, _ := body["events"].([]any)
	unread, _ := body["unread"].(float64)
	return list, unread
}

// An operator must be able to see what somebody else changed — the whole reason
// the feed is server-side rather than a list of toasts in one browser tab.
func TestFeedShowsAnotherOperatorsActions(t *testing.T) {
	e := newTestEnv(t)
	adminTok := e.login("admin@x.com", "adminpassword")
	e.do("POST", "/api/v1/users", adminTok, map[string]string{
		"email": "op@x.com", "name": "Op", "role": "operator", "password": "operatorpw1",
	})
	opTok := e.login("op@x.com", "operatorpw1")

	// The admin does something consequential.
	if rec, _ := e.do("PUT", "/api/v1/groups/default", adminTok, map[string]any{
		"name": "Default", "config": map[string]any{"general": map[string]any{"displayMode": "webview"}},
	}); rec.Code != http.StatusOK {
		t.Fatalf("policy save: status %d (%s)", rec.Code, rec.Body.String())
	}

	list, unread := e.feed(t, opTok)
	if len(list) == 0 {
		t.Fatal("the operator sees an empty feed; the admin's policy save was not recorded")
	}
	first, _ := list[0].(map[string]any)
	if first["kind"] != "policy_updated" {
		t.Errorf("newest event kind = %v, want policy_updated", first["kind"])
	}
	// Attribution is the point: "who changed the policy" must be answerable.
	if first["actor"] != "Admin" {
		t.Errorf("actor = %v, want the admin's name", first["actor"])
	}
	if unread < 1 {
		t.Errorf("unread = %v, want at least 1", unread)
	}
}

// Read markers are per-operator: clearing one badge must not clear another's.
func TestReadMarkerIsPerOperator(t *testing.T) {
	e := newTestEnv(t)
	adminTok := e.login("admin@x.com", "adminpassword")
	e.do("POST", "/api/v1/users", adminTok, map[string]string{
		"email": "op@x.com", "role": "operator", "password": "operatorpw1",
	})
	opTok := e.login("op@x.com", "operatorpw1")

	e.do("PUT", "/api/v1/groups/default", adminTok, map[string]any{
		"name": "Default", "config": map[string]any{"a": 1},
	})

	if _, unread := e.feed(t, opTok); unread == 0 {
		t.Fatal("precondition: operator should have unread events")
	}
	if rec, _ := e.do("POST", "/api/v1/events/read", opTok, map[string]any{}); rec.Code != http.StatusOK {
		t.Fatalf("mark read: status %d", rec.Code)
	}
	if _, unread := e.feed(t, opTok); unread != 0 {
		t.Errorf("operator unread after marking read = %v, want 0", unread)
	}
	if _, unread := e.feed(t, adminTok); unread == 0 {
		t.Error("the admin's badge was cleared by the operator reading theirs")
	}
}

// A stale tab must not resurrect notifications a newer one already cleared.
func TestReadMarkerNeverMovesBackwards(t *testing.T) {
	e := newTestEnv(t)
	tok := e.login("admin@x.com", "adminpassword")
	e.do("PUT", "/api/v1/groups/default", tok, map[string]any{"name": "D", "config": map[string]any{"a": 1}})
	e.do("POST", "/api/v1/events/read", tok, map[string]any{})
	if _, unread := e.feed(t, tok); unread != 0 {
		t.Fatal("precondition: everything should be read")
	}
	// A tab that loaded before those events reports an older high-water mark.
	e.do("POST", "/api/v1/events/read", tok, map[string]any{"up_to": 1})
	if _, unread := e.feed(t, tok); unread != 0 {
		t.Errorf("unread = %v after a stale tab reported an older marker, want 0", unread)
	}
}

// Heartbeats must never reach the feed. At a 30s interval one tablet alone
// would bury every real event within the hour.
func TestHeartbeatsAreNotEvents(t *testing.T) {
	e := newTestEnv(t)
	tok := e.login("admin@x.com", "adminpassword")

	if err := e.st.UpsertGroup(&store.Group{
		ID: "default", Name: "Default", Config: "{}", ConfigHash: "h", ConfigVersion: 1,
	}); err != nil {
		t.Fatal(err)
	}
	key := e.newDeviceKey(t, "tablet-1")

	before, _ := e.feed(t, tok)
	for i := 0; i < 5; i++ {
		if rec, _ := e.do("POST", "/api/v1/devices/tablet-1/heartbeat", key, map[string]any{}); rec.Code != http.StatusOK {
			t.Fatalf("heartbeat: status %d", rec.Code)
		}
	}
	after, _ := e.feed(t, tok)
	if len(after) != len(before) {
		t.Errorf("feed grew from %d to %d across five heartbeats", len(before), len(after))
	}
}

// seedEvents writes n entries so paging has something to walk.
func (e *testEnv) seedEvents(t *testing.T, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		sev := store.EventInfo
		if i%5 == 0 {
			sev = store.EventWarn
		}
		if err := e.st.RecordEvent(&store.Event{
			Kind: "seeded", Severity: sev, Actor: "test",
			Summary: "event " + strconv.Itoa(i),
		}); err != nil {
			t.Fatal(err)
		}
	}
}

// Paging must reach history the first page cannot show, without skipping or
// repeating an entry — the reason the cursor is an id and not an offset.
func TestPagingWalksTheWholeHistory(t *testing.T) {
	e := newTestEnv(t)
	tok := e.login("admin@x.com", "adminpassword")
	e.seedEvents(t, 120)

	seen := map[float64]bool{}
	var before float64
	pages := 0
	for {
		url := "/api/v1/events?limit=25"
		if before > 0 {
			url += "&before=" + strconv.Itoa(int(before))
		}
		rec, body := e.do("GET", url, tok, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("page %d: status %d", pages, rec.Code)
		}
		list, _ := body["events"].([]any)
		if len(list) == 0 {
			break
		}
		for _, item := range list {
			ev := item.(map[string]any)
			id := ev["id"].(float64)
			if seen[id] {
				t.Fatalf("id %v appeared on more than one page", id)
			}
			seen[id] = true
			before = id
		}
		pages++
		if body["has_more"] != true {
			break
		}
		if pages > 20 {
			t.Fatal("paging did not terminate")
		}
	}
	if len(seen) != 120 {
		t.Errorf("walked %d entries, want all 120", len(seen))
	}
}

// The first page must say there is more, so the console can offer to load it.
func TestFirstPageReportsMoreAndTotals(t *testing.T) {
	e := newTestEnv(t)
	tok := e.login("admin@x.com", "adminpassword")
	e.seedEvents(t, 60)

	_, body := e.do("GET", "/api/v1/events?limit=25", tok, nil)
	if body["has_more"] != true {
		t.Errorf("has_more = %v with 60 entries and a page of 25, want true", body["has_more"])
	}
	if n, _ := body["total"].(float64); n < 60 {
		t.Errorf("total = %v, want at least the 60 seeded", body["total"])
	}
	list, _ := body["events"].([]any)
	if len(list) != 25 {
		t.Errorf("page held %d entries, want exactly the 25 asked for", len(list))
	}
}

// Filtering to what needs attention must include errors, not just warnings:
// an operator hunting failures does not care which label it landed under.
func TestSeverityFilterCoversWarningsAndErrors(t *testing.T) {
	e := newTestEnv(t)
	tok := e.login("admin@x.com", "adminpassword")
	for _, sev := range []string{store.EventInfo, store.EventWarn, store.EventError, store.EventInfo} {
		if err := e.st.RecordEvent(&store.Event{Kind: "k", Severity: sev, Summary: sev}); err != nil {
			t.Fatal(err)
		}
	}
	_, body := e.do("GET", "/api/v1/events?severity=warn", tok, nil)
	list, _ := body["events"].([]any)
	if len(list) != 2 {
		t.Fatalf("filter returned %d entries, want the warning and the error", len(list))
	}
	for _, item := range list {
		if s := item.(map[string]any)["severity"]; s == store.EventInfo {
			t.Errorf("an info entry survived the filter: %v", item)
		}
	}
}
