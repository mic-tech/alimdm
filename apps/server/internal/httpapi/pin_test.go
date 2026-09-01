package httpapi

import (
	"net/http"
	"testing"
	"time"

	"ali-mdm/server/internal/auth"
	"ali-mdm/server/internal/config"
	"ali-mdm/server/internal/store"
)

// heartbeatOnce enrols a device against the given group config and returns the
// first heartbeat response, which is the one carrying the config to apply.
func heartbeatOnce(t *testing.T, groupConfig string) map[string]any {
	t.Helper()
	e := newTestEnv(t)

	canonical, err := config.Canonical([]byte(groupConfig))
	if err != nil {
		t.Fatal(err)
	}
	if err := e.st.UpsertGroup(&store.Group{
		ID: "default", Name: "Default", Config: string(canonical),
		ConfigHash: config.Hash(canonical), ConfigVersion: 1,
	}); err != nil {
		t.Fatal(err)
	}

	key, _ := auth.GenerateAPIKey()
	if err := e.st.CreateDevice(&store.Device{
		ID: "tablet-1", GroupID: "default", APIKeyHash: auth.HashAPIKey(key),
		LastAppliedHash: "stale", // forces sync_action = apply
		CreatedAt:       time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		t.Fatal(err)
	}

	rec, body := e.do("POST", "/api/v1/devices/tablet-1/heartbeat", key, map[string]any{})
	if rec.Code != http.StatusOK {
		t.Fatalf("heartbeat: status %d (%s)", rec.Code, rec.Body.String())
	}
	return body
}

// The console stores the kiosk PIN at sensitive.pin. The server used to look for
// it at general.pin, which nothing wrote — so no device was ever sent a PIN and
// every one of them accepted the hard-coded fallback instead. Verified on a real
// tablet: a PIN set in the console was rejected while the old default worked.
func TestPinFromSensitiveReachesTheDevice(t *testing.T) {
	body := heartbeatOnce(t, `{"general":{"displayMode":"webview"},"sensitive":{"pin":"8371"}}`)

	sc, ok := body["sensitive_config"].(map[string]any)
	if !ok || sc == nil {
		t.Fatalf("sensitive_config missing; the device would never receive a PIN: %v", body["sensitive_config"])
	}
	if sc["pin"] != "8371" {
		t.Fatalf("want the configured PIN, got %v", sc["pin"])
	}

	// And it must not travel in the plain config, which is logged and echoed back.
	cfg, _ := body["config"].(map[string]any)
	if sec, ok := cfg["sensitive"].(map[string]any); ok {
		if _, present := sec["pin"]; present {
			t.Fatal("PIN must be stripped from the plain config")
		}
	}
}

// An older config that kept the PIN under general.pin must keep working.
func TestPinFromGeneralStillHonoured(t *testing.T) {
	body := heartbeatOnce(t, `{"general":{"displayMode":"webview","pin":"4242"}}`)

	sc, ok := body["sensitive_config"].(map[string]any)
	if !ok || sc["pin"] != "4242" {
		t.Fatalf("legacy general.pin should still be delivered, got %v", body["sensitive_config"])
	}
	cfg, _ := body["config"].(map[string]any)
	if g, ok := cfg["general"].(map[string]any); ok {
		if _, present := g["pin"]; present {
			t.Fatal("PIN must be stripped from the plain config")
		}
	}
}

// No PIN configured means no sensitive_config, rather than an empty one.
func TestNoPinMeansNoSensitiveConfig(t *testing.T) {
	body := heartbeatOnce(t, `{"general":{"displayMode":"webview"},"sensitive":{"pin":""}}`)
	if sc := body["sensitive_config"]; sc != nil {
		t.Fatalf("expected no sensitive_config when no PIN is set, got %v", sc)
	}
}

// A tablet nobody has named still has to say which one it is on its own screen.
// The fallback lives on the server so it applies to the fleet as it stands.
func TestKioskLabelFallsBackToTheId(t *testing.T) {
	body := heartbeatOnce(t, `{"general":{"displayMode":"webview"}}`)
	if body["device_label"] != "tablet-1" {
		t.Errorf("device_label = %v for an unnamed tablet, want its id", body["device_label"])
	}
}
