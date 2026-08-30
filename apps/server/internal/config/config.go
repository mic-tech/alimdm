package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// Hash is the SHA-256 hex of a config's canonical JSON bytes. Ali MDM's client
// hashes the exact config string it stores, so the server must hash the same bytes
// it will hand back. We always re-marshal to canonical form before hashing.
func Hash(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// Canonical re-encodes a config object to stable JSON (sorted keys via encoding/json
// is not guaranteed, so we hash the exact bytes we store/serve — consistency matters
// more than cross-language canonicality here, since only our server hashes it).
func Canonical(raw []byte) ([]byte, error) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	return json.Marshal(v)
}

// ManagedApp is one entry in general.managedApps.
type ManagedApp struct {
	PackageName        string `json:"packageName"`
	DisplayName        string `json:"displayName"`
	ShowOnHomeScreen   bool   `json:"showOnHomeScreen"`
	LaunchOnBoot       bool   `json:"launchOnBoot"`
	KeepAlive          bool   `json:"keepAlive"`
	AllowAccessibility bool   `json:"allowAccessibility"`
}

// LockdownTemplate builds a Ali MDM structured config that locks the device to the
// given apps: external_app display mode, kiosk enabled, factory reset blocked, Ali MDM
// pinned as default launcher, and each app kept alive on boot.
func LockdownTemplate(apps []ManagedApp) (string, error) {
	cfg := map[string]any{
		"general": map[string]any{
			"displayMode":  "external_app",
			"externalApp":  map[string]any{"package": "", "mode": "single", "testMode": false},
			"managedApps":  apps,
			"dashboardMode": true,
		},
		"display": map[string]any{
			"keepScreenOn":    true,
			"defaultBrightness": 0.6,
		},
		"security": map[string]any{
			"kioskEnabled":       true,
			"blockFactoryReset":  true,
			"defaultLauncher":    true,
			"allowPowerButton":   false,
			"returnMode":         "tap_anywhere",
			"returnTapCount":     5,
			"pinMode":            "numeric",
			"backButtonMode":     "test",
			"autoRelaunchApp":    true,
		},
		"advanced": map[string]any{
			"restApi": map[string]any{"enabled": false},
			"mqtt":    map[string]any{"enabled": false},
		},
	}
	// If there's a primary app, set it as the externalApp package too.
	if len(apps) > 0 {
		cfg["general"].(map[string]any)["externalApp"] = map[string]any{
			"package": apps[0].PackageName, "mode": "single", "testMode": false,
		}
	}
	b, err := json.Marshal(cfg)
	return string(b), err
}
