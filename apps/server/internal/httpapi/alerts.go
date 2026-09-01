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
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"ali-mdm/server/internal/store"
)

const defaultOfflineMinutes = 15

type AlertWatcher struct {
	st       *store.Store
	interval time.Duration
	grace    time.Duration
	baseURL  string
	client   *http.Client
}

// NewAlertWatcher always returns a watcher. Whether it does anything is decided
// per tick from the settings the operator holds in the console, so turning
// alerting on or changing where it points needs no redeploy.
func NewAlertWatcher(st *store.Store, baseURL string) *AlertWatcher {
	return &AlertWatcher{
		st:       st,
		baseURL:  baseURL,
		interval: time.Minute,
		// Long enough for a device on a 30s heartbeat to check in a few times
		// after a deploy before anything is called offline.
		grace:  3 * time.Minute,
		client: &http.Client{Timeout: 15 * time.Second},
	}
}

// config reads the current webhook settings. An empty URL means alerting is off.
func (a *AlertWatcher) config() (string, time.Duration) {
	url, _ := a.st.GetSetting(store.SettingAlertWebhookURL)
	url = strings.TrimSpace(url)
	minutes := defaultOfflineMinutes
	if raw, _ := a.st.GetSetting(store.SettingAlertOfflineMinutes); raw != "" {
		if v, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil && v > 0 {
			minutes = v
		}
	}
	return url, time.Duration(minutes) * time.Minute
}

func (a *AlertWatcher) Start(ctx context.Context) {
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
	webhookURL, threshold := a.config()
	// Note there is no early return when the webhook is unset. Detection still
	// runs, because the console's notification feed shows these transitions and
	// must not depend on whether anyone has configured a webhook.
	devices, err := a.st.ListDevices()
	if err != nil {
		return
	}
	states, err := a.st.AlertStates()
	if err != nil {
		return
	}
	// The feed keeps its own transition state. device_alerts only advances when a
	// webhook delivery succeeds, so sharing it would drop feed entries whenever
	// alerting was off or the endpoint was down.
	eventStates, err := a.st.DeviceEventStates()
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

		// Feed first, and independently of delivery.
		wasLogged := eventStates[d.ID]
		if wasLogged == "" {
			wasLogged = store.AlertOK
		}
		label := deviceLabel(d.ID, d.Name)
		switch {
		case silent > threshold && wasLogged != store.AlertOffline:
			a.onEvent("device_offline", store.EventWarn, d.ID,
				fmt.Sprintf("%s stopped checking in (%s ago)", label, roundDuration(silent)))
			_ = a.st.SetDeviceEventState(d.ID, store.AlertOffline)
		case silent <= threshold && wasLogged == store.AlertOffline:
			a.onEvent("device_recovered", store.EventInfo, d.ID, label+" is checking in again")
			_ = a.st.SetDeviceEventState(d.ID, store.AlertOK)
		}

		if webhookURL == "" {
			continue
		}
		switch {
		case silent > threshold && was != store.AlertOffline:
			if a.notify(webhookURL, "device_offline", d, silent) {
				_ = a.st.SetAlertState(d.ID, store.AlertOffline)
			}
		case silent <= threshold && was == store.AlertOffline:
			if a.notify(webhookURL, "device_recovered", d, silent) {
				_ = a.st.SetAlertState(d.ID, store.AlertOK)
			}
		}
	}
}

// notify posts one event. The state is only advanced when this succeeds, so a
// webhook that is briefly unreachable retries on the next tick instead of
// silently swallowing the alert.
func (a *AlertWatcher) notify(webhookURL, event string, d store.Device, silent time.Duration) bool {
	name := d.Name
	if name == "" {
		name = d.ID
	}
	minutes := int(silent.Minutes())
	text := "Ali MDM: " + name + " has not checked in for " + strconv.Itoa(minutes) + " minutes."
	switch event {
	case "device_recovered":
		text = "Ali MDM: " + name + " is checking in again."
	case "test":
		text = "Ali MDM: test alert — offline notifications are working."
	}
	body, err := json.Marshal(map[string]any{
		"event":          event,
		"device_id":      d.ID,
		"device_name":    name,
		"model":          d.Model,
		"last_seen":      d.LastSeen,
		"silent_minutes": minutes,
		"console_url":    a.baseURL + "/devices",
		// Slack, Discord and Mattermost all render a bare "text" field, so one
		// payload works with the common targets without a per-service adapter.
		"text": text,
	})
	if err != nil {
		return false
	}
	req, err := http.NewRequest(http.MethodPost, webhookURL, bytes.NewReader(body))
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

// ── Console-managed settings ─────────────────────────────────────────────────

// getAlertSettings returns the current alerting configuration.
func (s *Server) getAlertSettings(w http.ResponseWriter, r *http.Request) {
	url, _ := s.st.GetSetting(store.SettingAlertWebhookURL)
	mins, _ := s.st.GetSetting(store.SettingAlertOfflineMinutes)
	if strings.TrimSpace(mins) == "" {
		mins = strconv.Itoa(defaultOfflineMinutes)
	}
	writeJSON(w, map[string]any{
		"webhook_url":     url,
		"offline_minutes": mins,
		"enabled":         strings.TrimSpace(url) != "",
	})
}

// updateAlertSettings stores the configuration. An empty URL turns alerting off,
// which is the only way to disable it and so must be allowed through.
func (s *Server) updateAlertSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		WebhookURL     string `json:"webhook_url"`
		OfflineMinutes string `json:"offline_minutes"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil {
		writeErr(w, http.StatusBadRequest, "request body is not valid JSON")
		return
	}
	url := strings.TrimSpace(req.WebhookURL)
	if url != "" && !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		writeErr(w, http.StatusBadRequest, "webhook URL must start with http:// or https://")
		return
	}
	mins := strings.TrimSpace(req.OfflineMinutes)
	if mins != "" {
		v, err := strconv.Atoi(mins)
		if err != nil || v <= 0 {
			writeErr(w, http.StatusBadRequest, "offline minutes must be a positive whole number")
			return
		}
		// Below the heartbeat interval every device looks offline between beats.
		if v < 2 {
			writeErr(w, http.StatusBadRequest, "offline minutes must be at least 2, or every device alerts between heartbeats")
			return
		}
	}
	if err := s.st.SetSetting(store.SettingAlertWebhookURL, url); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not save settings")
		return
	}
	if mins != "" {
		if err := s.st.SetSetting(store.SettingAlertOfflineMinutes, mins); err != nil {
			writeErr(w, http.StatusInternalServerError, "could not save settings")
			return
		}
	}
	s.getAlertSettings(w, r)
}

// testAlertWebhook posts a sample payload so an operator can confirm the
// endpoint works without waiting for a device to actually go silent.
func (s *Server) testAlertWebhook(w http.ResponseWriter, r *http.Request) {
	url, _ := s.st.GetSetting(store.SettingAlertWebhookURL)
	if strings.TrimSpace(url) == "" {
		writeErr(w, http.StatusBadRequest, "save a webhook URL first")
		return
	}
	watcher := NewAlertWatcher(s.st, s.baseURL)
	sample := store.Device{ID: "test", Name: "Webhook test", Model: "—",
		LastSeen: time.Now().UTC().Format(time.RFC3339)}
	if !watcher.notify(strings.TrimSpace(url), "test", sample, 0) {
		writeErr(w, http.StatusBadGateway, "the webhook did not accept the message — check the URL and the server log")
		return
	}
	writeJSON(w, map[string]any{"sent": true})
}

// onEvent writes a transition into the console's notification feed. Like every
// other event write this is best-effort: a feed that cannot be written must not
// stop alerting from running.
func (a *AlertWatcher) onEvent(kind, severity, deviceID, summary string) {
	if err := a.st.RecordEvent(&store.Event{
		Kind: kind, Severity: severity, Actor: "system",
		DeviceID: deviceID, Summary: summary,
	}); err != nil {
		log.Printf("events: could not record %s: %v", kind, err)
	}
}

// roundDuration renders a silence the way someone reading the feed would say
// it, rather than as 17m43.019s.
func roundDuration(d time.Duration) string {
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dd%dh", int(d.Hours())/24, int(d.Hours())%24)
}
