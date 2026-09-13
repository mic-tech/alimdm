package httpapi

import (
	"net/http"
	"testing"
)

// The serial arrives on the heartbeat and reaches the device page, and a
// heartbeat from an older build that sends none does not wipe it out.
func TestHeartbeatSerialReachesDeviceDetail(t *testing.T) {
	e := newTestEnv(t)
	tok := e.login("admin@x.com", "adminpassword")
	if err := e.st.UpsertGroup(&testGroup); err != nil {
		t.Fatal(err)
	}
	id, key := enrollOnce(t, e, "TB330FU")
	path := "/api/v1/devices/" + id

	serialOf := func() any {
		t.Helper()
		rec, body := e.do("GET", path, tok, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("detail: status %d (%s)", rec.Code, rec.Body.String())
		}
		return body["serial"]
	}

	if got := serialOf(); got != "" {
		t.Errorf("serial before any report = %v, want empty", got)
	}
	if rec, _ := e.do("POST", path+"/heartbeat", key, map[string]any{
		"system": map[string]any{"serial_number": " HA1XYZ42 "},
	}); rec.Code != http.StatusOK {
		t.Fatalf("heartbeat: status %d (%s)", rec.Code, rec.Body.String())
	}
	if got := serialOf(); got != "HA1XYZ42" {
		t.Errorf("serial = %v, want the reported HA1XYZ42", got)
	}
	e.do("POST", path+"/heartbeat", key, map[string]any{"system": map[string]any{}})
	if got := serialOf(); got != "HA1XYZ42" {
		t.Errorf("serial after a heartbeat without one = %v, want it kept", got)
	}
}
