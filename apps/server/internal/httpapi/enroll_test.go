package httpapi

import (
	"net/http"
	"testing"
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
