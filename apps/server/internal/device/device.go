package device

import "time"

// Heartbeat is what a device POSTs each poll interval.
type Heartbeat struct {
	ConfigHash string `json:"config_hash"`
	Battery    int    `json:"battery"`
	Wifi       int    `json:"wifi"` // 0/1
	AndroidVer string `json:"android_ver"`
}

// Response is what the server returns to a heartbeat.
type Response struct {
	ConfigHash string         `json:"config_hash"`
	Config     map[string]any `json:"config,omitempty"` // full policy when changed
	Poke       []Poke         `json:"poke,omitempty"`   // pending commands (e.g. install)
}

type Poke struct {
	Type    string `json:"type"` // "install" | "uninstall" | "reboot" | "lock"
	Package string `json:"package,omitempty"`
	URL     string `json:"url,omitempty"`
	SHA256  string `json:"sha256,omitempty"`
}

// Online reports whether a device was seen within the given window.
func Online(lastSeen string, window time.Duration) bool {
	if lastSeen == "" {
		return false
	}
	t, err := time.Parse(time.RFC3339, lastSeen)
	if err != nil {
		return false
	}
	return time.Since(t) < window
}
