package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"
)

// qrExtras pulls the admin-extras bundle out of a QR response. The payload is
// the literal text encoded into the QR, so it arrives as a JSON *string* — a
// test that treats it as an object passes whatever the value really is.
func qrExtras(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	raw, ok := body["payload"].(string)
	if !ok {
		t.Fatalf("payload is %T, want the QR text as a string", body["payload"])
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("payload is not valid JSON: %v", err)
	}
	extras, ok := payload["android.app.extra.PROVISIONING_ADMIN_EXTRAS_BUNDLE"].(map[string]any)
	if !ok {
		t.Fatalf("no admin extras bundle in the payload: %v", payload)
	}
	return extras
}

// A QR can name the tablet it provisions, through the same admin-extras bundle
// that already carries the token and group.
func TestProvisionQRCarriesTheLabel(t *testing.T) {
	e := newTestEnv(t)
	tok := e.login("admin@x.com", "adminpassword")

	rec, body := e.do("GET", "/api/v1/provision/qr?label=Library%20tablet", tok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("qr: status %d (%s)", rec.Code, rec.Body.String())
	}
	if got := qrExtras(t, body)["device_label"]; got != "Library tablet" {
		t.Errorf("device_label = %v, want the label from the query", got)
	}
}

// Without one, no label is sent and the tablet shows as its id.
func TestProvisionQRWithoutALabel(t *testing.T) {
	e := newTestEnv(t)
	tok := e.login("admin@x.com", "adminpassword")
	_, body := e.do("GET", "/api/v1/provision/qr", tok, nil)
	if got, present := qrExtras(t, body)["device_label"]; present {
		t.Errorf("device_label should be absent when none was asked for, got %v", got)
	}
}

// The group chosen when generating the code has to reach the tablet, or a QR
// enrolment silently lands in the default group.
func TestProvisionQRCarriesTheGroup(t *testing.T) {
	e := newTestEnv(t)
	tok := e.login("admin@x.com", "adminpassword")
	if err := e.st.UpsertGroup(&testGroup); err != nil {
		t.Fatal(err)
	}
	_, body := e.do("GET", "/api/v1/provision/qr?group=default", tok, nil)
	if got := qrExtras(t, body)["group_id"]; got != "default" {
		t.Errorf("group_id = %v, want default", got)
	}
}
