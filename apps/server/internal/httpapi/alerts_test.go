package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"ali-mdm/server/internal/store"
)

type captured struct {
	mu     sync.Mutex
	events []map[string]any
}

func (c *captured) add(m map[string]any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, m)
}

func (c *captured) all() []map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]map[string]any(nil), c.events...)
}

// newAlertEnv wires a watcher to a stub webhook and a device whose last check-in
// is `silentFor` ago.
func newAlertEnv(t *testing.T, silentFor time.Duration, status int) (*AlertWatcher, *store.Store, *captured) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "alerts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	if err := st.CreateDevice(&store.Device{
		ID: "tablet-1", Name: "Front desk", GroupID: "default", APIKeyHash: "x",
		LastSeen:  time.Now().Add(-silentFor).UTC().Format(time.RFC3339),
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		t.Fatal(err)
	}

	got := &captured{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		got.add(m)
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)

	return NewAlertWatcher(st, srv.URL, "https://mdm.example.com", 15), st, got
}

func TestAlertFiresOnceWhileDeviceStaysSilent(t *testing.T) {
	w, st, got := newAlertEnv(t, 30*time.Minute, http.StatusOK)

	// A tablet switched off overnight must not produce an alert per tick.
	w.check()
	w.check()
	w.check()

	events := got.all()
	if len(events) != 1 {
		t.Fatalf("expected exactly one alert while silent, got %d", len(events))
	}
	if events[0]["event"] != "device_offline" {
		t.Fatalf("unexpected event: %v", events[0])
	}
	if events[0]["device_name"] != "Front desk" {
		t.Fatalf("alert should name the device, got %v", events[0]["device_name"])
	}
	states, _ := st.AlertStates()
	if states["tablet-1"] != store.AlertOffline {
		t.Fatalf("state should record the alert, got %q", states["tablet-1"])
	}
}

func TestAlertRecoveryIsReported(t *testing.T) {
	w, st, got := newAlertEnv(t, 30*time.Minute, http.StatusOK)
	w.check()

	// The device checks in again.
	if err := st.UpdateHeartbeat("tablet-1", "", 80, 0, "15", "TB330FU",
		time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	w.check()
	w.check() // must not repeat the recovery

	events := got.all()
	if len(events) != 2 {
		t.Fatalf("expected offline + recovery, got %d events", len(events))
	}
	if events[1]["event"] != "device_recovered" {
		t.Fatalf("second event should be recovery, got %v", events[1])
	}
	states, _ := st.AlertStates()
	if states["tablet-1"] != store.AlertOK {
		t.Fatalf("state should be cleared after recovery, got %q", states["tablet-1"])
	}
}

func TestHealthyDeviceNeverAlerts(t *testing.T) {
	w, _, got := newAlertEnv(t, time.Minute, http.StatusOK)
	w.check()
	if n := len(got.all()); n != 0 {
		t.Fatalf("a device seen a minute ago must not alert, got %d events", n)
	}
}

// A webhook that is down must not silently swallow the alert: the state stays
// unreported so the next tick tries again.
func TestFailedWebhookRetriesNextTick(t *testing.T) {
	w, st, got := newAlertEnv(t, 30*time.Minute, http.StatusInternalServerError)
	w.check()

	states, _ := st.AlertStates()
	if states["tablet-1"] == store.AlertOffline {
		t.Fatal("state must not advance when the webhook rejected the alert")
	}
	w.check()
	if n := len(got.all()); n != 2 {
		t.Fatalf("expected a retry on the next tick, got %d attempts", n)
	}
}

// A freshly enrolled device has never reported; treating that as "offline"
// would alert on every enrolment.
func TestDeviceThatNeverReportedIsNotOffline(t *testing.T) {
	w, st, got := newAlertEnv(t, time.Minute, http.StatusOK)
	if err := st.CreateDevice(&store.Device{
		ID: "tablet-new", GroupID: "default", APIKeyHash: "y",
		LastSeen: "", CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		t.Fatal(err)
	}
	w.check()
	for _, e := range got.all() {
		if e["device_id"] == "tablet-new" {
			t.Fatal("a device that has never checked in must not alert")
		}
	}
}
