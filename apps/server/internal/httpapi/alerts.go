package httpapi

// Offline alerting.
//
// A tablet that stops checking in is still a tablet that looks fine: the kiosk
// keeps rendering, nobody in the room notices, and the fleet quietly stops
// being managed. The console shows last_seen, but only to whoever is looking.
// This watcher turns that into something that arrives.
//
// Three things keep it from becoming noise:
//
//   - A longer threshold than the console's Online badge. Three minutes is
//     right for a badge but far too twitchy to notify on: a reboot or a Wi-Fi
//     blip would fire it.
//   - Alerts on transition only, with a matching recovery, so a tablet switched
//     off overnight produces two messages rather than hundreds. The state is in
//     the database, so a server restart does not re-alert a fleet.
//   - A grace period after start-up. Every device looks silent when the server
//     has just come back; without this, a deploy would page about the whole
//     fleet.

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"ali-mdm/server/internal/store"
)

type AlertWatcher struct {
	st         *store.Store
	webhookURL string
	threshold  time.Duration
	interval   time.Duration
	grace      time.Duration
	baseURL    string
	client     *http.Client
}

// NewAlertWatcher returns nil when no webhook is configured, so callers can
// simply not start it.
func NewAlertWatcher(st *store.Store, webhookURL, baseURL string, thresholdMinutes int) *AlertWatcher {
	webhookURL = strings.TrimSpace(webhookURL)
	if webhookURL == "" {
		return nil
	}
	if thresholdMinutes <= 0 {
		thresholdMinutes = 15
	}
	return &AlertWatcher{
		st:         st,
		webhookURL: webhookURL,
		baseURL:    baseURL,
		threshold:  time.Duration(thresholdMinutes) * time.Minute,
		interval:   time.Minute,
		// Long enough for a device on a 30s heartbeat to check in a few times
		// after a deploy before anything is called offline.
		grace:  3 * time.Minute,
		client: &http.Client{Timeout: 15 * time.Second},
	}
}

func (a *AlertWatcher) Start(ctx context.Context) {
	log.Printf("offline alerts: webhook configured, threshold %s", a.threshold)
	go func() {
		startedAt := time.Now()
		t := time.NewTicker(a.interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if time.Since(startedAt) < a.grace {
					continue
				}
				a.check()
			}
		}
	}()
}

func (a *AlertWatcher) check() {
	devices, err := a.st.ListDevices()
	if err != nil {
		return
	}
	states, err := a.st.AlertStates()
	if err != nil {
		return
	}
	now := time.Now()
	for _, d := range devices {
		// A device that has never reported cannot be "offline" yet — it has not
		// had a first chance. Alerting here would fire on every new enrolment.
		if d.LastSeen == "" {
			continue
		}
		seen, err := time.Parse(time.RFC3339, d.LastSeen)
		if err != nil {
			continue
		}
		silent := now.Sub(seen)
		was := states[d.ID]
		if was == "" {
			was = store.AlertOK
		}

		switch {
		case silent > a.threshold && was != store.AlertOffline:
			if a.notify("device_offline", d, silent) {
				_ = a.st.SetAlertState(d.ID, store.AlertOffline)
			}
		case silent <= a.threshold && was == store.AlertOffline:
			if a.notify("device_recovered", d, silent) {
				_ = a.st.SetAlertState(d.ID, store.AlertOK)
			}
		}
	}
}

// notify posts one event. The state is only advanced when this succeeds, so a
// webhook that is briefly unreachable retries on the next tick instead of
// silently swallowing the alert.
func (a *AlertWatcher) notify(event string, d store.Device, silent time.Duration) bool {
	name := d.Name
	if name == "" {
		name = d.ID
	}
	minutes := int(silent.Minutes())
	text := "Ali MDM: " + name + " has not checked in for " + strconv.Itoa(minutes) + " minutes."
	if event == "device_recovered" {
		text = "Ali MDM: " + name + " is checking in again."
	}
	body, err := json.Marshal(map[string]any{
		"event":          event,
		"device_id":      d.ID,
		"device_name":    name,
		"model":          d.Model,
		"last_seen":      d.LastSeen,
		"silent_minutes": minutes,
		"console_url":    a.baseURL + "/console/",
		// Slack, Discord and Mattermost all render a bare "text" field, so one
		// payload works with the common targets without a per-service adapter.
		"text": text,
	})
	if err != nil {
		return false
	}
	req, err := http.NewRequest(http.MethodPost, a.webhookURL, bytes.NewReader(body))
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.client.Do(req)
	if err != nil {
		log.Printf("offline alerts: webhook failed for %s: %v", d.ID, err)
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		log.Printf("offline alerts: webhook returned %d for %s", resp.StatusCode, d.ID)
		return false
	}
	log.Printf("offline alerts: sent %s for %s", event, d.ID)
	return true
}
