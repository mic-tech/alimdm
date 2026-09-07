package httpapi

import (
	"net/http"
	"strings"
	"testing"

	"ali-mdm/server/internal/store"
)

// enrollOnce posts an enrolment with no serial_number, the way a client that
// cannot report one behaves.
func enrollOnce(t *testing.T, e *testEnv, model string) (string, string) {
	t.Helper()
	rec, body := e.do("POST", "/api/v1/devices/enroll", "", map[string]any{
		"token": "enroll",
		"device_info": map[string]any{
			"model":         model,
			"serial_number": "",
		},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("enroll: status %d (%s)", rec.Code, rec.Body.String())
	}
	id, _ := body["device_id"].(string)
	key, _ := body["api_key"].(string)
	if id == "" || key == "" {
		t.Fatalf("enroll returned id=%q key=%q", id, key)
	}
	return id, key
}

// A whole fleet shares one enrolment token. Deriving the device id from that
// token made every tablet collapse into a single record, each enrolment
// silently overwriting the previous one's API key.
func TestEnrollWithoutSerialGivesDistinctDevices(t *testing.T) {
	e := newTestEnv(t)

	id1, key1 := enrollOnce(t, e, "tablet-a")
	id2, key2 := enrollOnce(t, e, "tablet-b")

	if id1 == id2 {
		t.Fatalf("two tablets sharing an enrolment token collided on id %q", id1)
	}
	if key1 == key2 {
		t.Fatalf("two tablets were issued the same API key")
	}

	devs, err := e.st.ListDevices()
	if err != nil {
		t.Fatal(err)
	}
	if len(devs) != 2 {
		t.Fatalf("expected 2 device rows, got %d", len(devs))
	}
}

// A client that does report a serial must keep it as its id, so re-enrolling
// the same tablet updates its row instead of creating a duplicate.
func TestEnrollWithSerialIsStable(t *testing.T) {
	e := newTestEnv(t)

	for i := 0; i < 2; i++ {
		rec, body := e.do("POST", "/api/v1/devices/enroll", "", map[string]any{
			"token": "enroll",
			"device_info": map[string]any{
				"model":         "TB330FU",
				"serial_number": "HA2743FG",
			},
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("enroll %d: status %d (%s)", i, rec.Code, rec.Body.String())
		}
		if id, _ := body["device_id"].(string); id != "HA2743FG" {
			t.Fatalf("enroll %d: want id HA2743FG, got %q", i, id)
		}
	}

	devs, err := e.st.ListDevices()
	if err != nil {
		t.Fatal(err)
	}
	if len(devs) != 1 {
		t.Fatalf("re-enrolling one tablet should not duplicate it; got %d rows", len(devs))
	}
}

func TestEnrollRejectsBadToken(t *testing.T) {
	e := newTestEnv(t)
	rec, _ := e.do("POST", "/api/v1/devices/enroll", "", map[string]any{
		"token":       "not-the-token",
		"device_info": map[string]any{"serial_number": "X1"},
	})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 for a bad enrolment token, got %d", rec.Code)
	}
}

// A tablet can arrive already named, so it does not have to be matched up by
// serial after the fact.
func TestEnrolmentAcceptsALabel(t *testing.T) {
	e := newTestEnv(t)
	rec, body := e.do("POST", "/api/v1/devices/enroll", "", map[string]any{
		"token":        "enroll",
		"device_label": "Library tablet",
		"device_info":  map[string]any{"serial_number": "tablet-1", "model": "TB330FU"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("enrol: status %d (%s)", rec.Code, rec.Body.String())
	}
	_ = body
	d, err := e.st.GetDevice("tablet-1")
	if err != nil {
		t.Fatal(err)
	}
	if d.Name != "Library tablet" {
		t.Errorf("name = %q, want the label given at enrolment", d.Name)
	}
}

// Without a label the device is genuinely unnamed, so the console and the kiosk
// both fall back to the id. It used to be named after the model, which meant
// that fallback never happened and every new tablet read as "TB330FU".
func TestEnrolmentWithoutALabelLeavesTheDeviceUnnamed(t *testing.T) {
	e := newTestEnv(t)
	e.do("POST", "/api/v1/devices/enroll", "", map[string]any{
		"token":       "enroll",
		"device_info": map[string]any{"serial_number": "tablet-1", "model": "TB330FU"},
	})
	d, err := e.st.GetDevice("tablet-1")
	if err != nil {
		t.Fatal(err)
	}
	if d.Name != "" {
		t.Errorf("name = %q for an unlabelled enrolment, want empty so it falls back to the id", d.Name)
	}
}

// enrollWith posts an enrolment carrying whatever group and label a QR would
// have packed into it, and returns the feed entry it produced.
func enrollWith(t *testing.T, e *testEnv, serial, group, label string) map[string]any {
	t.Helper()
	body := map[string]any{
		"token":       "enroll",
		"device_info": map[string]any{"serial_number": serial},
	}
	if group != "" {
		body["group_id"] = group
	}
	if label != "" {
		body["device_label"] = label
	}
	if rec, _ := e.do("POST", "/api/v1/devices/enroll", "", body); rec.Code != http.StatusOK {
		t.Fatalf("enroll: status %d", rec.Code)
	}
	events, err := e.st.ListEvents(10)
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range events {
		if ev.Kind == "device_enrolled" && ev.DeviceID == serial {
			return map[string]any{"summary": ev.Summary, "severity": ev.Severity}
		}
	}
	t.Fatalf("no enrolment event for %s", serial)
	return nil
}

// A tablet that arrives with no group and no label used to read exactly like
// one whose group was dropped on the way: "Enrolled X into default", both
// times. That ambiguity is the whole reason a lost QR extra went unnoticed, so
// the feed now says which of the two happened.
func TestTheFeedSaysWhatAnEnrolmentAskedFor(t *testing.T) {
	e := newTestEnv(t)
	adminTok := e.login("admin@x.com", "adminpassword")
	if rec, _ := e.do("POST", "/api/v1/groups", adminTok, map[string]any{
		"id": "iqra", "name": "IQRA",
	}); rec.Code != http.StatusOK && rec.Code != http.StatusCreated {
		t.Fatalf("create group: status %d (%s)", rec.Code, rec.Body.String())
	}

	got := enrollWith(t, e, "tab-honoured", "iqra", "Library tablet")
	summary, _ := got["summary"].(string)
	if !strings.Contains(summary, "into iqra") || !strings.Contains(summary, "Library tablet") {
		t.Errorf("honoured enrolment reads %q; it should name the group and the label", summary)
	}

	got = enrollWith(t, e, "tab-bare", "", "")
	summary, _ = got["summary"].(string)
	if !strings.Contains(summary, "no group and no label") {
		t.Errorf("bare enrolment reads %q; it should say nothing was asked for", summary)
	}

	// A group that has since been deleted, or a typo in a hand-made QR. Silently
	// landing in default is what made this invisible in the first place.
	got = enrollWith(t, e, "tab-unknown", "gone", "")
	summary, _ = got["summary"].(string)
	if !strings.Contains(summary, "into default") || !strings.Contains(summary, "gone") {
		t.Errorf("unknown-group enrolment reads %q; it should name the group it could not honour", summary)
	}
	if got["severity"] != store.EventWarn {
		t.Errorf("severity = %v for an ignored group, want warn — it is a misconfiguration, not routine", got["severity"])
	}
}
