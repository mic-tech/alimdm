package httpapi

import (
	"net/http"
	"testing"
	"time"

	"ali-mdm/server/internal/auth"
	"ali-mdm/server/internal/store"
)

// seedDevice inserts a device directly so the test does not depend on the
// enrolment handler.
func seedDevice(t *testing.T, e *testEnv, id string) string {
	t.Helper()
	key, _ := auth.GenerateAPIKey()
	err := e.st.CreateDevice(&store.Device{
		ID: id, GroupID: "default", APIKeyHash: auth.HashAPIKey(key),
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		t.Fatal(err)
	}
	return key
}

// The whole point of a forced unenrolment is that it needs nothing from the
// tablet: a device that is lost or broken must still be removable.
func TestForceUnenrollRevokesDeviceWithoutIt(t *testing.T) {
	e := newTestEnv(t)
	tok := e.login("admin@x.com", "adminpassword")
	key := seedDevice(t, e, "dead-tablet")

	// The device's key works while it is enrolled.
	rec, _ := e.do("POST", "/api/v1/devices/dead-tablet/heartbeat", key, map[string]any{})
	if rec.Code == http.StatusUnauthorized {
		t.Fatalf("device key should work before unenrolment, got %d", rec.Code)
	}

	rec, _ = e.do("DELETE", "/api/v1/devices/dead-tablet", tok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("unenroll: status %d (%s)", rec.Code, rec.Body.String())
	}

	// Gone from the console.
	devs, err := e.st.ListDevices()
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range devs {
		if d.ID == "dead-tablet" {
			t.Fatal("device still listed after forced unenrolment")
		}
	}

	// And its key no longer authenticates — this 401 is what makes a tablet
	// that resurfaces wipe its own cloud credentials.
	rec, _ = e.do("POST", "/api/v1/devices/dead-tablet/heartbeat", key, map[string]any{})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("revoked key should be rejected with 401, got %d", rec.Code)
	}
}

func TestForceUnenrollRequiresOperator(t *testing.T) {
	e := newTestEnv(t)
	seedDevice(t, e, "tablet-1")

	rec, _ := e.do("DELETE", "/api/v1/devices/tablet-1", "", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 without a token, got %d", rec.Code)
	}
	if _, err := e.st.GetDevice("tablet-1"); err != nil {
		t.Fatal("device must survive an unauthorised unenrol attempt")
	}
}

func TestForceUnenrollUnknownDeviceIs404(t *testing.T) {
	e := newTestEnv(t)
	tok := e.login("admin@x.com", "adminpassword")
	rec, _ := e.do("DELETE", "/api/v1/devices/never-existed", tok, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404 for an unknown device, got %d", rec.Code)
	}
}
