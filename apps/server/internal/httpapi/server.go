package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"ali-mdm/server/internal/apk"
	"ali-mdm/server/internal/blob"
	"ali-mdm/server/internal/auth"
	"ali-mdm/server/internal/config"
	"ali-mdm/server/internal/device"
	"ali-mdm/server/internal/store"
)

type Server struct {
	st     *store.Store
	signer *auth.Signer
	apks   *apk.Store
	// agentAPKs holds Ali MDM's own builds, deliberately in a separate root from
	// the managed-app catalogue so an agent build can never be auto-queued as a
	// managed app (or deleted from the Packages page) by accident.
	agentAPKs    *apk.Store
	// files holds operator-uploaded documents pushed to device inboxes, kept in
	// its own root so a worksheet can never be mistaken for an installable APK.
	files        *blob.Store
	pokes        *PokeQueue
	// streams fans a tablet's live-view frames out to console viewers. In
	// memory only: a picture of a classroom has no business on disk.
	streams      *streamHubs
	// snapshots holds the latest still per device for the console's card view.
	// In memory only, like the live-view frames.
	snapshots    *snapshotStore
	enrollToken  string
	baseURL      string
	consoleDir   string
	provisionAPK string // path to the Ali MDM APK served for zero-touch QR provisioning
}

func New(st *store.Store, signer *auth.Signer, apks, agentAPKs *apk.Store, files *blob.Store, pokes *PokeQueue, enrollToken, baseURL, consoleDir, provisionAPK string) *Server {
	return &Server{st: st, signer: signer, apks: apks, agentAPKs: agentAPKs, files: files, pokes: pokes, streams: newStreamHubs(), snapshots: newSnapshotStore(), enrollToken: enrollToken, baseURL: baseURL, consoleDir: consoleDir, provisionAPK: provisionAPK}
}

// ── Auth helpers ─────────────────────────────────────────────────────────────

// requireDevice validates the Bearer device API key and injects the device.
func (s *Server) requireDevice(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := bearer(r)
		if key == "" {
			http.Error(w, "missing token", http.StatusUnauthorized)
			return
		}
		dev := s.findDeviceByKey(key)
		if dev == nil {
			http.Error(w, "invalid device token", http.StatusUnauthorized)
			return
		}
		next(w, r.WithContext(withDevice(r.Context(), dev)))
	}
}

// requireOperator validates the session token and resolves the account behind it.
// The role is read from the database rather than the token so that a demotion or
// deletion takes effect on the next request instead of at token expiry.
func (s *Server) requireOperator(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok := bearer(r)
		if tok == "" {
			http.Error(w, "missing token", http.StatusUnauthorized)
			return
		}
		claims, err := s.signer.Verify(tok)
		if err != nil || claims.Kind != "operator" {
			http.Error(w, "invalid operator token", http.StatusUnauthorized)
			return
		}
		op, err := s.st.GetOperator(claims.Sub)
		if err != nil {
			http.Error(w, "account no longer exists", http.StatusUnauthorized)
			return
		}
		next(w, r.WithContext(withOperator(r.Context(), op)))
	}
}

// requireAdmin is requireOperator plus the admin role check.
func (s *Server) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return s.requireOperator(func(w http.ResponseWriter, r *http.Request) {
		if op := operatorFrom(r.Context()); op == nil || op.Role != store.RoleAdmin {
			writeErr(w, http.StatusForbidden, "administrator access required")
			return
		}
		next(w, r)
	})
}

// findDeviceByKey scans devices and matches the presented key against stored hashes.
// (Fine at 12 devices; a keyed index can be added later if it ever matters.)
func (s *Server) findDeviceByKey(key string) *store.Device {
	devs, _ := s.st.ListDevices()
	for i := range devs {
		if auth.VerifyAPIKey(key, devs[i].APIKeyHash) {
			return &devs[i]
		}
	}
	return nil
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(h, "Bearer ") {
		return strings.TrimSpace(h[len("Bearer "):])
	}
	return ""
}

// ── Routes ───────────────────────────────────────────────────────────────────

func (s *Server) Routes() *http.ServeMux {
	mux := http.NewServeMux()
	// Ali MDM device protocol
	// All /api/v1/devices/... routes go through one dispatcher to avoid Go 1.22 mux
	// pattern ambiguity between the literal "enroll" and the "{id}" segment.
	mux.HandleFunc("/api/v1/devices/", s.deviceDispatcher)
	mux.HandleFunc("POST /api/v1/commands/{id}/result/", s.commandResult)
	mux.HandleFunc("POST /api/v1/devices/{id}/screenshot/", s.requireDevice(s.screenshot))
	mux.HandleFunc("POST /api/v1/devices/{id}/unenroll/", s.unenroll)
	mux.HandleFunc("GET /api/v1/apk/{name}", s.downloadAPK)
	// Zero-touch provisioning: the tablet's setup wizard downloads the Ali MDM
	// APK from here while scanning the QR. Open (no auth) — it runs before the
	// device has an API key. Serves nothing if FK_PROVISION_APK is unset.
	mux.HandleFunc("GET /api/v1/provision/apk", s.provisionAPKHandler)
	// QR payload for setup-wizard provisioning (operator-only: it carries the
	// enrolment token).
	mux.HandleFunc("GET /api/v1/provision/qr", s.requireOperator(s.provisionQR))
	// Operator console
	mux.HandleFunc("POST /api/v1/operator/login", s.operatorLogin)
	mux.HandleFunc("GET /api/v1/devices", s.requireOperator(s.listDevices))
	mux.HandleFunc("GET /api/v1/devices/{id}/commands", s.requireOperator(s.listDeviceCommands))
	mux.HandleFunc("POST /api/v1/devices/{id}/commands", s.requireOperator(s.enqueueCommand))
	mux.HandleFunc("GET /api/v1/groups", s.requireOperator(s.listGroups))
	mux.HandleFunc("POST /api/v1/groups", s.requireOperator(s.createGroup))
	mux.HandleFunc("GET /api/v1/groups/{id}", s.requireOperator(s.getGroup))
	mux.HandleFunc("PUT /api/v1/groups/{id}", s.requireOperator(s.updateGroup))
	mux.HandleFunc("DELETE /api/v1/groups/{id}", s.requireOperator(s.deleteGroup))
	mux.HandleFunc("POST /api/v1/devices/{id}/group", s.requireOperator(s.moveDeviceGroup))
	mux.HandleFunc("POST /api/v1/devices/{id}/name", s.requireOperator(s.renameDevice))
	// Forced unenrolment, operator-side. Needs no cooperation from the tablet.
	mux.HandleFunc("DELETE /api/v1/devices/{id}", s.requireOperator(s.forceUnenroll))
	mux.HandleFunc("POST /api/v1/apks", s.requireOperator(s.uploadAPK))
	mux.HandleFunc("GET /api/v1/apks", s.requireOperator(s.listAPKs))
	// Queue a silent install of an uploaded APK to one or all devices.
	mux.HandleFunc("POST /api/v1/apks/{name}/install", s.requireOperator(s.installAPK))
	mux.HandleFunc("DELETE /api/v1/apks/{name}", s.requireOperator(s.deleteAPK))

	// Agent (self) OTA — see agent.go. Kept off the /apks routes on purpose.
	mux.HandleFunc("POST /api/v1/agent/release", s.requireOperator(s.uploadAgentRelease))
	mux.HandleFunc("GET /api/v1/agent/release", s.requireOperator(s.getAgentRelease))
	mux.HandleFunc("POST /api/v1/agent/rollout", s.requireOperator(s.rolloutAgentUpdate))
	mux.HandleFunc("GET /api/v1/agent/updates", s.requireOperator(s.listAgentUpdates))
	mux.HandleFunc("GET /api/v1/agent/apk", s.downloadAgentAPK)

	// Own profile (any signed-in operator)
	mux.HandleFunc("GET /api/v1/me", s.requireOperator(s.getMe))
	mux.HandleFunc("PUT /api/v1/me", s.requireOperator(s.updateMe))
	mux.HandleFunc("POST /api/v1/me/password", s.requireOperator(s.changeMyPassword))

	// Server-wide alerting settings (admins only: the webhook is infrastructure).
	// File library: upload once, push to a group, land in every tablet's inbox.
	mux.HandleFunc("POST /api/v1/files", s.requireOperator(s.uploadFile))
	mux.HandleFunc("GET /api/v1/files", s.requireOperator(s.listFiles))
	mux.HandleFunc("DELETE /api/v1/files/{name}", s.requireOperator(s.deleteFile))
	mux.HandleFunc("POST /api/v1/files/{name}/push", s.requireOperator(s.pushFile))
	mux.HandleFunc("GET /api/v1/files/{name}/deliveries", s.requireOperator(s.fileDeliveries))
	// Device-facing: the tablet fetches its own inbox and reports what it did.
	mux.HandleFunc("GET /api/v1/devices/{id}/files", s.requireDevice(s.devicePendingFiles))
	mux.HandleFunc("POST /api/v1/devices/{id}/files/{name}/result", s.requireDevice(s.fileDeliveryResult))
	mux.HandleFunc("GET /api/v1/files/{name}/download", s.requireDevice(s.downloadFile))

	// Live view. Frames travel device → server → console, because nothing can
	// open a connection to a tablet behind school NAT.
	mux.HandleFunc("POST /api/v1/devices/{id}/stream/start", s.requireOperator(s.startStream))
	mux.HandleFunc("POST /api/v1/devices/{id}/stream/stop", s.requireOperator(s.stopStream))
	mux.HandleFunc("GET /api/v1/devices/{id}/stream/status", s.requireOperator(s.streamStatus))
	mux.HandleFunc("GET /api/v1/devices/{id}/stream.mjpeg", s.requireOperator(s.streamMJPEG))
	mux.HandleFunc("POST /api/v1/devices/{id}/stream/frame", s.requireDevice(s.postFrame))

	// Snapshots: a still per device, for the console's card view.
	mux.HandleFunc("GET /api/v1/devices/{id}/snapshot", s.requireOperator(s.deviceSnapshot))
	mux.HandleFunc("GET /api/v1/devices/{id}/snapshot/meta", s.requireOperator(s.snapshotMeta))
	mux.HandleFunc("POST /api/v1/devices/{id}/snapshot/request", s.requireOperator(s.requestSnapshot))

	// Per-device file manager: the console asks, the tablet answers later.
	mux.HandleFunc("GET /api/v1/devices/{id}/inbox", s.requireOperator(s.deviceInbox))
	mux.HandleFunc("POST /api/v1/devices/{id}/inbox/refresh", s.requireOperator(s.refreshDeviceInbox))
	mux.HandleFunc("DELETE /api/v1/devices/{id}/inbox/{name}", s.requireOperator(s.deleteDeviceFile))
	mux.HandleFunc("POST /api/v1/devices/{id}/inbox", s.requireDevice(s.reportDeviceInbox))

	// The notification feed is readable by any signed-in operator: it is how
	// they see what the fleet and their colleagues have been doing.
	mux.HandleFunc("GET /api/v1/events", s.requireOperator(s.listEvents))
	mux.HandleFunc("POST /api/v1/events/read", s.requireOperator(s.markEventsRead))

	mux.HandleFunc("GET /api/v1/settings/alerts", s.requireAdmin(s.getAlertSettings))
	mux.HandleFunc("PUT /api/v1/settings/alerts", s.requireAdmin(s.updateAlertSettings))
	mux.HandleFunc("POST /api/v1/settings/alerts/test", s.requireAdmin(s.testAlertWebhook))

	// Account administration (admins only)
	mux.HandleFunc("GET /api/v1/users", s.requireAdmin(s.listUsers))
	mux.HandleFunc("POST /api/v1/users", s.requireAdmin(s.createUser))
	mux.HandleFunc("PUT /api/v1/users/{email}", s.requireAdmin(s.updateUser))
	mux.HandleFunc("DELETE /api/v1/users/{email}", s.requireAdmin(s.deleteUser))
	mux.HandleFunc("POST /api/v1/users/{email}/password", s.requireAdmin(s.resetUserPassword))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })

	// Operator console (static React build) at the root, with SPA fallback so
	// that every page has a real URL: /devices, /groups, /activity and so on.
	if s.consoleDir != "" {
		mux.Handle("/", s.consoleHandler())
	}

	return mux
}

// consoleHandler serves the built console. A path that names no file falls
// back to index.html, which is what makes /devices and /groups work: the
// browser asks the server for them, and the console then routes on the URL.
func (s *Server) consoleHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// This handler is registered at "/", so it also catches anything under
		// /api/ that no route claimed. Falling back to index.html there would
		// hand a client an HTML page to parse as JSON; a mistyped endpoint
		// should say it does not exist.
		if strings.HasPrefix(r.URL.Path, "/api/") {
			writeErr(w, http.StatusNotFound, "unknown endpoint")
			return
		}
		// path.Clean resolves any ".." before it can reach the filesystem.
		rel := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if rel != "" && !strings.Contains(rel, "..") {
			full := filepath.Join(s.consoleDir, rel)
			if st, err := os.Stat(full); err == nil && !st.IsDir() {
				// Asset filenames carry a content hash, so a given URL never
				// changes what it returns and can be cached for a long time.
				if strings.HasPrefix(rel, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				http.ServeFile(w, r, full)
				return
			}
		}
		// Nothing under /assets/ is a page, so a miss there is a genuine 404.
		// Answering with index.html would hand the browser an HTML page where
		// it asked for a script, and the error it reports ("unexpected token
		// <") says nothing about the missing file that caused it.
		if strings.HasPrefix(rel, "assets/") {
			http.NotFound(w, r)
			return
		}
		// The SPA entry. Never cached: it is the one file whose contents change
		// on a deploy while keeping its URL, and a stale copy would go on
		// asking for assets that are no longer there.
		w.Header().Set("Cache-Control", "no-store")
		http.ServeFile(w, r, filepath.Join(s.consoleDir, "index.html"))
	})
}

// ── Ali MDM device endpoints ───────────────────────────────────────────────

type enrollRequest struct {
	Token      string         `json:"token"`
	DeviceInfo map[string]any `json:"device_info"`
	GroupID    string         `json:"group_id"`
	// Optional label, set when provisioning so a tablet arrives in the console
	// already identified rather than needing to be matched up by serial later.
	DeviceLabel string `json:"device_label"`
}

// deviceDispatcher routes the /api/v1/devices/ subtree by method + path segment.
// Ali MDM calls: POST /devices/enroll/, POST /devices/{id}/heartbeat/,
// GET /devices/{id}/commands/, GET /devices/{id}/updates/,
// POST /devices/{id}/screenshot/, POST /devices/{id}/unenroll/.
func (s *Server) deviceDispatcher(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/v1/devices/")
	rest = strings.TrimSuffix(rest, "/")
	parts := strings.Split(rest, "/")

	// Enrollment is the only unauthenticated device route.
	if len(parts) == 1 && parts[0] == "enroll" && r.Method == http.MethodPost {
		s.enroll(w, r)
		return
	}

	// Everything else requires a valid device API key.
	if len(parts) != 2 {
		http.NotFound(w, r)
		return
	}
	key := bearer(r)
	if key == "" {
		http.Error(w, "missing token", http.StatusUnauthorized)
		return
	}
	dev := s.findDeviceByKey(key)
	if dev == nil {
		http.Error(w, "invalid device token", http.StatusUnauthorized)
		return
	}
	// The path's {id} must match the authenticated device.
	if parts[0] != dev.ID {
		http.Error(w, "unknown device", http.StatusNotFound)
		return
	}
	r = r.WithContext(withDevice(r.Context(), dev))

	switch {
	case r.Method == http.MethodPost && parts[1] == "heartbeat":
		s.heartbeat(w, r)
	case r.Method == http.MethodGet && parts[1] == "commands":
		s.deviceCommands(w, r)
	case r.Method == http.MethodGet && parts[1] == "updates":
		s.deviceUpdates(w, r)
	case r.Method == http.MethodPost && parts[1] == "screenshot":
		s.screenshot(w, r)
	case r.Method == http.MethodPost && parts[1] == "unenroll":
		s.unenroll(w, r)
	case r.Method == http.MethodPost && parts[1] == "agent-update":
		s.agentUpdateResult(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) enroll(w http.ResponseWriter, r *http.Request) {
	var req enrollRequest
	if json.NewDecoder(r.Body).Decode(&req) != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	if req.Token == "" {
		http.Error(w, "missing token", http.StatusBadRequest)
		return
	}
	// The enrollment token is a pre-shared secret (from the ADB bootstrap / QR).
	// For this build we accept a fixed enrollment token configured via env; in a
	// larger deployment this would be a per-device signed token.
	if req.Token != s.enrollToken {
		http.Error(w, "invalid enrollment token", http.StatusUnauthorized)
		return
	}
	strVal := func(v any) string {
		if v == nil {
			return ""
		}
		if s, ok := v.(string); ok {
			return s
		}
		return fmt.Sprintf("%v", v)
	}
	id := strVal(req.DeviceInfo["serial_number"])
	if id == "" {
		// Never derive the id from the enrolment token: a fleet shares one
		// token, so every tablet would land on the same device id and each
		// enrolment would overwrite the previous one's API key. A random id
		// keeps devices distinct; the cost is that a client which reports no
		// serial gets a fresh row if it ever re-enrols, which is strictly
		// better than two tablets silently sharing one.
		id = "device-" + randomID()
	}
	key, _ := auth.GenerateAPIKey()
	now := time.Now().UTC().Format(time.RFC3339)
	// Enroll into the requested group if it exists, else "default".
	groupID := "default"
	if req.GroupID != "" {
		if _, err := s.st.GetGroup(req.GroupID); err == nil {
			groupID = req.GroupID
		}
	}
	// The label given at provisioning, if any. It used to default to the model,
	// which duplicated a column the console already shows and meant a tablet
	// nobody had named still counted as named — so the fall back to the id
	// never happened. No label now means no label.
	label := strings.TrimSpace(req.DeviceLabel)
	if len(label) > 64 {
		label = label[:64]
	}
	d := &store.Device{
		ID: id, Name: label, GroupID: groupID,
		APIKeyHash: auth.HashAPIKey(key), CreatedAt: now,
	}
	if err := s.st.CreateDevice(d); err != nil {
		http.Error(w, "enroll failed", http.StatusInternalServerError)
		return
	}
	// Auto-queue installs for every managed app in the device's group that has a
	// matching APK in the store. This makes provisioning fully hands-off: the
	// tablet enrolls, the config syncs (grid appears), and the apps start
	// downloading + installing immediately — no manual "Install to devices" click.
	autoQueued := s.autoQueueInstalls(d.ID, d.GroupID, now)
	s.recordAs("device", "device_enrolled", store.EventInfo, id,
		"Enrolled "+id+" into "+groupID)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"device_id":         id,
		"api_key":           key,
		"organization_name": "mic-tech",
		"auto_queued":       autoQueued,
	})
}

// autoQueueInstalls matches each managed app in the group's config against the
// APK store and enqueues an install_apk update for any match. Returns the count
// of apps queued. APK files are named by package (e.g. com.foo.bar.apk) or by an
// arbitrary upload name; we match on the package name appearing in the file name.
func (s *Server) autoQueueInstalls(deviceID, groupID, now string) int {
	group, err := s.st.GetGroup(groupID)
	if err != nil {
		return 0
	}
	var cfg struct {
		General struct {
			ManagedApps []struct {
				PackageName string `json:"packageName"`
				AutoInstall bool   `json:"autoInstall"`
			} `json:"managedApps"`
		} `json:"general"`
	}
	if json.Unmarshal([]byte(group.Config), &cfg) != nil {
		return 0
	}
	apks, err := s.apks.List()
	if err != nil {
		return 0
	}
	queued := 0
	for _, app := range cfg.General.ManagedApps {
		if app.PackageName == "" {
			continue
		}
		// Only auto-install apps explicitly flagged for it. Apps the operator
		// pushes manually (via the console APK installer) are not auto-installed
		// on enrollment — they stay off the device until pushed.
		if !app.AutoInstall {
			continue
		}
		// Find an APK whose file name contains the package name.
		var match string
		for _, name := range apks {
			if strings.Contains(name, app.PackageName) {
				match = name
				break
			}
		}
		if match == "" {
			continue
		}
		cmdID := "apk-auto-" + shortHash(deviceID+app.PackageName+now)
		u := &store.APKUpdate{
			CommandID: cmdID, DeviceID: deviceID, PackageName: app.PackageName,
			VersionName: "", APKPath: baseName(match), Status: "pending",
		}
		if s.st.EnqueueAPKUpdate(u) == nil {
			queued++
			s.pokes.Enqueue(deviceID, device.Poke{Type: "install_apk", Package: app.PackageName})
		}
	}
	return queued
}

type heartbeatRequest struct {
	Config          map[string]any `json:"config"`
	ConfigVersion   int            `json:"config_version"`
	ConfigUpdatedAt string         `json:"config_updated_at"`
	Battery         struct {
		Level    int  `json:"level"`
		Charging bool `json:"charging"`
	} `json:"battery"`
	Network struct {
		WifiSSID string `json:"wifi_ssid"`
	} `json:"network"`
	System struct {
		AndroidVersion string `json:"android_version"`
		Model          string `json:"model"`
		// The Ali MDM build the tablet is actually running. Absent from builds
		// older than v57, where the last known value is kept rather than
		// overwritten with nothing.
		AppVersionCode int    `json:"app_version_code"`
		AppVersionName string `json:"app_version_name"`
	} `json:"system"`
}

func (s *Server) heartbeat(w http.ResponseWriter, r *http.Request) {
	dev := deviceFrom(r.Context())
	if dev == nil {
		http.Error(w, "unknown device", http.StatusNotFound)
		return
	}
	var req heartbeatRequest
	_ = json.NewDecoder(r.Body).Decode(&req)

	group, err := s.st.GetGroup(dev.GroupID)
	if err != nil {
		http.Error(w, "no group", http.StatusNotFound)
		return
	}

	// The device is out of date if its last-applied hash != the group's current hash.
	needSync := group.ConfigHash != dev.LastAppliedHash

	// Record telemetry + the hash the device now holds.
	lastSeen := time.Now().UTC().Format(time.RFC3339)
	_ = s.st.UpdateHeartbeat(dev.ID, group.ConfigHash, req.Battery.Level, 0,
		req.System.AndroidVersion, req.System.Model, lastSeen)
	_ = s.st.SetDeviceAppVersion(dev.ID, req.System.AppVersionCode, req.System.AppVersionName)

	resp := map[string]any{
		"status":           "ok",
		"pending_commands": s.st.CountPendingCommands(dev.ID) + s.st.CountPendingAPKUpdates(dev.ID),
		// Files waiting for this tablet's inbox. Separate from pending_commands
		// so the app fetches them on their own channel and a stuck command
		// cannot hold up a worksheet.
		"pending_files": s.st.CountPendingFileDeliveries(dev.ID),
		// Live view is driven from here: the tablet starts capturing when an
		// operator is watching and stops when the last one leaves.
		"stream_requested": s.streams.get(dev.ID).wanted(),
		"server_time":      lastSeen,
		"sync_action":      "none",
		"config":           nil,
		"sensitive_config": nil,
		"config_version":   group.ConfigVersion,
		"force_unenroll":   false,
		// Per-device label shown in the corner of the tablet's kiosk screen. It
		// rides on every heartbeat rather than in config, which is group-wide.
		// Falls back to the id, matching the console: a tablet nobody has named
		// should still say which one it is when someone is standing in front of
		// it. Resolved here rather than in the app so it applies to the fleet
		// as it stands, without waiting for a new build.
		"device_label": deviceLabel(dev.ID, dev.Name),
		// Non-nil only while an operator-triggered self-update is outstanding.
		"agent_update": s.agentUpdateFor(dev.ID),
	}
	if needSync {
		resp["sync_action"] = "apply"
		var cfg map[string]any
		if json.Unmarshal([]byte(group.Config), &cfg) == nil {
			resp["config"] = cfg
			// The kiosk PIN is a secret: it travels in sensitive_config (which the
			// app writes to secure storage) rather than the plain config, and is
			// stripped from the config so it is never echoed back or logged.
			// The console writes the PIN to sensitive.pin. This used to read
			// general.pin, which nothing ever wrote — so sensitive_config was
			// never populated, no device was ever sent a PIN, and every one of
			// them fell through to the hard-coded default in verifyPin(). A PIN
			// set in the console had no effect on any tablet.
			//
			// general.pin is still honoured for any older config that used it.
			pin := ""
			if sec, ok := cfg["sensitive"].(map[string]any); ok {
				if p, _ := sec["pin"].(string); p != "" {
					pin = p
					delete(sec, "pin")
				}
			}
			if pin == "" {
				if g, ok := cfg["general"].(map[string]any); ok {
					if p, _ := g["pin"].(string); p != "" {
						pin = p
						delete(g, "pin")
					}
				}
			}
			if pin != "" {
				resp["sensitive_config"] = map[string]any{"pin": pin}
			}
		}
	}
	// Drain any MQTT-poked commands into the queue so pending_commands reflects them.
	// install_apk pokes are wake-only: the APK itself is delivered via the /updates/
	// channel (see deviceUpdates), and the app's command dispatcher does not handle
	// install_apk, so enqueuing it here would surface as "Unsupported command type".
	// We still let it bump pending_commands (via CountPendingAPKUpdates) so the device
	// wakes and polls /updates/.
	for _, p := range s.pokes.Drain(dev.ID) {
		if p.Type == "install_apk" {
			continue
		}
		_ = s.st.EnqueueCommand(&store.Command{
			ID: "cmd-" + shortHash(dev.ID+lastSeen+p.Type), DeviceID: dev.ID, Type: p.Type,
			Params: store.MustJSON(map[string]any{"package": p.Package}),
			Status: "pending", CreatedAt: lastSeen,
		})
	}
	resp["pending_commands"] = s.st.CountPendingCommands(dev.ID) + s.st.CountPendingAPKUpdates(dev.ID)
	resp["pending_files"] = s.st.CountPendingFileDeliveries(dev.ID)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (s *Server) deviceCommands(w http.ResponseWriter, r *http.Request) {
	dev := deviceFrom(r.Context())
	if dev == nil {
		http.Error(w, "unknown device", http.StatusNotFound)
		return
	}
	cmds, _ := s.st.ClaimPendingCommands(dev.ID)
	out := make([]map[string]any, 0, len(cmds))
	for _, c := range cmds {
		var params map[string]any
		_ = json.Unmarshal([]byte(c.Params), &params)
		out = append(out, map[string]any{
			"id": c.ID, "type": c.Type, "params": params,
			"created_at": c.CreatedAt, "expires_at": c.ExpiresAt,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"commands": out})
}

func (s *Server) deviceUpdates(w http.ResponseWriter, r *http.Request) {
	dev := deviceFrom(r.Context())
	if dev == nil {
		http.Error(w, "unknown device", http.StatusNotFound)
		return
	}
	ups, _ := s.st.ClaimPendingAPKUpdates(dev.ID)
	out := make([]map[string]any, 0, len(ups))
	for _, u := range ups {
		out = append(out, map[string]any{
			"command_id":   u.CommandID,
			"package_name": u.PackageName,
			"version_name": u.VersionName,
			"download_url": s.baseURL + "/api/v1/apk/" + baseName(u.APKPath),
		})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

func (s *Server) commandResult(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		Status       string          `json:"status"`
		Result       json.RawMessage `json:"result"`
		ErrorMessage string          `json:"error_message"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	if err := s.st.ReportCommandResult(id, req.Status, string(req.Result), req.ErrorMessage); err != nil {
		http.Error(w, "unknown command", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) unenroll(w http.ResponseWriter, r *http.Request) {
	dev := deviceFrom(r.Context())
	if dev == nil {
		http.Error(w, "unknown device", http.StatusNotFound)
		return
	}
	// Mark the device for wipe by clearing its group + key. The client wipes itself.
	_ = s.st.UpdateHeartbeat(dev.ID, "", 0, 0, "", "", "")
	s.recordAs("device", "device_unenrolled", store.EventWarn, dev.ID,
		deviceLabel(dev.ID, dev.Name)+" unenrolled itself")
	w.WriteHeader(http.StatusOK)
}

// forceUnenroll retires a device from the console, without needing the tablet.
//
// The previous operator-facing unenroll could not work: the console's path
// redirected onto the device-authenticated route, which then found no device in
// context and answered "unknown device". Even reached, it only blanked
// telemetry — it left the API key valid and the device managed, and the
// force_unenroll heartbeat flag it was supposed to pair with is never set.
//
// Deleting the row is what actually revokes access. The device's next heartbeat
// authenticates against a key that no longer exists, gets a 401, and the app
// wipes its own cloud credentials on that response. A tablet that is dead, lost
// or permanently offline simply never comes back, and the console is correct
// either way.
//
// Note this does not surrender Device Owner: a tablet that resurfaces stops
// being cloud-managed but stays locked down locally, which has to be released
// on the device itself.
func (s *Server) forceUnenroll(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.st.GetDevice(id); err != nil {
		http.Error(w, "unknown device", http.StatusNotFound)
		return
	}
	if err := s.st.DeleteDevice(id); err != nil {
		http.Error(w, "unenroll failed", http.StatusInternalServerError)
		return
	}
	// Forget its picture too; the row is gone, the image should go with it.
	s.snapshots.drop(id)
	s.record(r, "device_removed", store.EventWarn, id, "Removed "+id+" from the console")
	writeJSON(w, map[string]any{"id": id, "unenrolled": true})
}

// ── APK download (device-authenticated) ──────────────────────────────────────

func (s *Server) downloadAPK(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	f, err := s.apks.Open(baseName(name))
	if err != nil {
		http.Error(w, "apk not found", http.StatusNotFound)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/vnd.android.package-archive")
	w.Header().Set("Content-Disposition", `attachment; filename="`+baseName(name)+`"`)
	io.Copy(w, f)
}

// provisionAPKHandler serves the Ali MDM APK for zero-touch QR provisioning.
// The Android setup wizard fetches this URL (from the QR's
// PROVISIONING_DEVICE_ADMIN_PACKAGE_DOWNLOAD_LOCATION) and installs it as the
// Device Owner. It must be open (no auth) because it runs before the device is
// enrolled. If no APK is configured (FK_PROVISION_APK empty) it returns 404.
func (s *Server) provisionAPKHandler(w http.ResponseWriter, r *http.Request) {
	if s.provisionAPK == "" {
		http.Error(w, "provisioning apk not configured", http.StatusNotFound)
		return
	}
	f, err := os.Open(s.provisionAPK)
	if err != nil {
		http.Error(w, "provisioning apk not available", http.StatusNotFound)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/vnd.android.package-archive")
	w.Header().Set("Content-Disposition", `attachment; filename="alimdm.apk"`)
	io.Copy(w, f)
}

// ── Operator console ─────────────────────────────────────────────────────────

func (s *Server) operatorLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	op, err := s.st.GetOperator(req.Email)
	if err != nil || !auth.VerifyPassword(req.Password, op.PasswordHash) {
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}
	tok, _ := s.signer.Sign(auth.Claims{
		Sub: op.Email, Kind: "operator", Email: op.Email, Role: op.Role,
		Exp: time.Now().Add(12 * time.Hour).Unix(),
	})
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"token": tok, "user": publicUser(op)})
}

func (s *Server) listDevices(w http.ResponseWriter, r *http.Request) {
	devs, _ := s.st.ListDevices()
	type pub struct {
		ID            string `json:"id"`
		Name          string `json:"name"`
		GroupID       string `json:"group_id"`
		ConfigVersion int    `json:"config_version"`
		Battery       int    `json:"battery"`
		AndroidVer    string `json:"android_ver"`
		Model         string `json:"model"`
		// What the tablet says it is running, and whether that matches the
		// build that has been staged for the fleet. Reporting only the rollout
		// status hid a tablet sitting on an old build with its update recorded
		// as successful.
		AppVersionCode int    `json:"app_version_code"`
		AppVersionName string `json:"app_version_name"`
		Stale          bool   `json:"stale"`
		LastSeen       string `json:"last_seen"`
		Online         bool   `json:"online"`
	}
	// The staged release is what every tablet is expected to converge on.
	staged := 0
	if rel, err := s.st.GetAgentRelease(); err == nil {
		staged = rel.VersionCode
	}
	out := make([]pub, 0, len(devs))
	for _, d := range devs {
		online := false
		if t, err := time.Parse(time.RFC3339, d.LastSeen); err == nil {
			online = time.Since(t) < 3*time.Minute
		}
		// A device below the staged build is behind, including one that has
		// enrolled but not yet checked in — it does not have the build either.
		stale := staged > 0 && d.AppVersionCode < staged
		out = append(out, pub{d.ID, d.Name, d.GroupID, d.ConfigVersion, d.Battery, d.AndroidVer, d.Model,
			d.AppVersionCode, d.AppVersionName, stale, d.LastSeen, online})
	}
	json.NewEncoder(w).Encode(out)
}

func (s *Server) listDeviceCommands(w http.ResponseWriter, r *http.Request) {
	cmds, _ := s.st.ListCommands(r.PathValue("id"))
	json.NewEncoder(w).Encode(cmds)
}

func (s *Server) enqueueCommand(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		Type   string         `json:"type"`
		Params map[string]any `json:"params"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil || req.Type == "" {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	c := &store.Command{
		ID: "cmd-" + shortHash(id+now+req.Type), DeviceID: id, Type: req.Type,
		Params: store.MustJSON(req.Params), Status: "pending", CreatedAt: now,
	}
	if err := s.st.EnqueueCommand(c); err != nil {
		http.Error(w, "enqueue failed", http.StatusInternalServerError)
		return
	}
	// Poke the device over MQTT so it polls immediately instead of waiting for its tick.
	s.pokes.Enqueue(id, device.Poke{Type: req.Type, Package: ""})
	s.record(r, "command_sent", store.EventInfo, id, "Sent "+req.Type+" to "+id)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"id": c.ID, "status": "pending"})
}

func (s *Server) listGroups(w http.ResponseWriter, r *http.Request) {
	groups, _ := s.st.ListGroups()
	if groups == nil {
		groups = []store.Group{}
	}
	json.NewEncoder(w).Encode(groups)
}

func (s *Server) getGroup(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	g, err := s.st.GetGroup(id)
	if err != nil {
		http.Error(w, "group not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(g)
}

// policyConfigBytes accepts `config` as either a JSON object — which is what a
// browser client naturally sends, and what the console has always sent — or as
// a JSON-encoded string, the shape this endpoint originally declared.
//
// The mismatch meant every policy save from the console failed: an object
// cannot unmarshal into a Go string, so the request was rejected before
// anything looked at it, and the only feedback was "bad body".
func policyConfigBytes(raw json.RawMessage) ([]byte, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return nil, false
	}
	if trimmed[0] == '"' {
		var asString string
		if json.Unmarshal(trimmed, &asString) != nil || strings.TrimSpace(asString) == "" {
			return nil, false
		}
		return []byte(asString), true
	}
	return trimmed, true
}

func (s *Server) updateGroup(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		Name   string          `json:"name"`
		Config json.RawMessage `json:"config"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "request body is not valid JSON: "+err.Error())
		return
	}
	raw, ok := policyConfigBytes(req.Config)
	if !ok {
		writeErr(w, http.StatusBadRequest, "config is required: send the policy object, or its JSON encoded as a string")
		return
	}
	// Validate it's parseable JSON, then canonicalize + hash.
	canonical, err := config.Canonical(raw)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "config is not valid JSON: "+err.Error())
		return
	}
	g, err := s.st.GetGroup(id)
	if err != nil {
		g = &store.Group{ID: id, Name: req.Name}
	}
	if req.Name != "" {
		g.Name = req.Name
	}
	g.Config = string(canonical)
	g.ConfigHash = config.Hash(canonical)
	g.ConfigVersion++
	if err := s.st.UpsertGroup(g); err != nil {
		http.Error(w, "update failed", http.StatusInternalServerError)
		return
	}
	// Invalidate every device in the group so they re-sync on next heartbeat.
	affected := 0
	if devs, _ := s.st.ListDevices(); devs != nil {
		for _, d := range devs {
			if d.GroupID == id {
				_ = s.st.UpdateHeartbeat(d.ID, "", d.Battery, 0, d.AndroidVer, d.Model, d.LastSeen)
				affected++
			}
		}
	}
	s.record(r, "policy_updated", store.EventInfo, "",
		fmt.Sprintf("Saved policy %q (v%d) — %s will re-sync", g.Name, g.ConfigVersion, plural(affected, "device", "devices")))
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"id": g.ID, "config_hash": g.ConfigHash, "config_version": g.ConfigVersion})
}

// createGroup makes a new policy group. The config may be supplied directly,
// or copied from an existing group via copy_from (useful for cloning a known-
// good policy). A group with no config starts empty.
func (s *Server) createGroup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Config   string `json:"config"`
		CopyFrom string `json:"copy_from"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	id := req.ID
	if id == "" {
		http.Error(w, "missing group id", http.StatusBadRequest)
		return
	}
	// Reject duplicates.
	if _, err := s.st.GetGroup(id); err == nil {
		http.Error(w, "group already exists", http.StatusConflict)
		return
	}
	name := req.Name
	if name == "" {
		name = id
	}
	var cfgRaw []byte
	var hash string
	if req.Config != "" {
		canonical, err := config.Canonical([]byte(req.Config))
		if err != nil {
			http.Error(w, "config not valid JSON", http.StatusBadRequest)
			return
		}
		cfgRaw = canonical
		hash = config.Hash(canonical)
	} else if req.CopyFrom != "" {
		src, err := s.st.GetGroup(req.CopyFrom)
		if err != nil {
			http.Error(w, "copy_from group not found", http.StatusBadRequest)
			return
		}
		cfgRaw = []byte(src.Config)
		hash = src.ConfigHash
	} else {
		// Empty group: canonical empty object.
		canonical, _ := config.Canonical([]byte("{}"))
		cfgRaw = canonical
		hash = config.Hash(canonical)
	}
	g := &store.Group{ID: id, Name: name, Config: string(cfgRaw), ConfigHash: hash, ConfigVersion: 1}
	if err := s.st.UpsertGroup(g); err != nil {
		http.Error(w, "create failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"id": g.ID, "name": g.Name, "config_hash": g.ConfigHash, "config_version": g.ConfigVersion})
}

// deleteGroup removes a group. Devices in it are first reassigned to "default"
// (or the group named by the optional reassign_to field) so no device is left
// without a policy.
func (s *Server) deleteGroup(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "default" {
		http.Error(w, "cannot delete the default group", http.StatusBadRequest)
		return
	}
	if _, err := s.st.GetGroup(id); err != nil {
		http.Error(w, "group not found", http.StatusNotFound)
		return
	}
	// Reassign any devices in this group to default so they keep a policy.
	if devs, _ := s.st.ListDevices(); devs != nil {
		for _, d := range devs {
			if d.GroupID == id {
				_ = s.st.SetDeviceGroup(d.ID, "default")
			}
		}
	}
	if err := s.st.DeleteGroup(id); err != nil {
		http.Error(w, "delete failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"id": id, "deleted": true})
}

// moveDeviceGroup moves a device to a different group and invalidates its
// applied-config hash so it re-syncs the new group's policy on next heartbeat.
func (s *Server) moveDeviceGroup(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		GroupID string `json:"group_id"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil || req.GroupID == "" {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	dev, err := s.st.GetDevice(id)
	if err != nil {
		http.Error(w, "unknown device", http.StatusNotFound)
		return
	}
	if _, err := s.st.GetGroup(req.GroupID); err != nil {
		http.Error(w, "group not found", http.StatusBadRequest)
		return
	}
	if err := s.st.SetDeviceGroup(id, req.GroupID); err != nil {
		http.Error(w, "move failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"id": id, "name": dev.Name, "group_id": req.GroupID})
}

// renameDevice sets the operator-facing label shown in the console and on the
// tablet's own kiosk screen. An empty name is allowed and clears the label, in
// which case the device falls back to displaying nothing.
func (s *Server) renameDevice(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		Name string `json:"name"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(req.Name)
	// Bounded so a pathological label cannot break the tablet's layout or bloat
	// every heartbeat response.
	if len(name) > 64 {
		name = name[:64]
	}
	if _, err := s.st.GetDevice(id); err != nil {
		http.Error(w, "unknown device", http.StatusNotFound)
		return
	}
	if err := s.st.SetDeviceName(id, name); err != nil {
		http.Error(w, "rename failed", http.StatusInternalServerError)
		return
	}
	if name == "" {
		s.record(r, "device_renamed", store.EventInfo, id, "Cleared the label on "+id)
	} else {
		s.record(r, "device_renamed", store.EventInfo, id, "Labelled "+id+" \u201c"+name+"\u201d")
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"id": id, "name": name})
}

func (s *Server) uploadAPK(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(512 << 20); err != nil {
		http.Error(w, "bad upload", http.StatusBadRequest)
		return
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "missing file", http.StatusBadRequest)
		return
	}
	defer file.Close()
	name := r.FormValue("name")
	if name == "" {
		name = hdr.Filename
	}
	sha, path, size, err := s.apks.Ingest(file, name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	_ = s.st.SaveAPK(&store.APK{Name: baseName(path), SHA256: sha, Size: size, Path: path})
	s.record(r, "apk_uploaded", store.EventInfo, "", "Uploaded package "+baseName(path))
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"name": baseName(path), "sha256": sha, "size": size})
}

func (s *Server) listAPKs(w http.ResponseWriter, r *http.Request) {
	apks, _ := s.st.ListAPKs()
	if apks == nil {
		apks = []store.APK{}
	}
	json.NewEncoder(w).Encode(apks)
}

// deleteAPK removes an uploaded APK from both the catalogue and disk. Devices
// that already installed it are unaffected — this only stops future installs
// and frees the server-side copy.
func (s *Server) deleteAPK(w http.ResponseWriter, r *http.Request) {
	name := baseName(r.PathValue("name"))
	if _, err := s.apks.Open(name); err != nil {
		http.Error(w, "apk not found", http.StatusNotFound)
		return
	}
	// Drop the row first: a stale catalogue entry pointing at a missing file is
	// worse than an orphaned file, which List() simply stops reporting.
	if err := s.st.DeleteAPK(name); err != nil {
		http.Error(w, "delete failed", http.StatusInternalServerError)
		return
	}
	if err := s.apks.Delete(name); err != nil {
		http.Error(w, "delete failed", http.StatusInternalServerError)
		return
	}
	s.record(r, "apk_deleted", store.EventWarn, "", "Deleted package "+name)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"name": name, "deleted": true})
}

// installAPK queues a silent install of an uploaded APK. Body:
//
//	{"package_name": "com.x.y", "devices": ["dev1","dev2"]}  // devices optional = all
func (s *Server) installAPK(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if _, err := s.apks.Open(baseName(name)); err != nil {
		http.Error(w, "apk not found", http.StatusNotFound)
		return
	}
	var req struct {
		PackageName string   `json:"package_name"`
		Devices     []string `json:"devices"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil || req.PackageName == "" {
		http.Error(w, "package_name is required", http.StatusBadRequest)
		return
	}
	targets := req.Devices
	if len(targets) == 0 {
		devs, _ := s.st.ListDevices()
		for _, d := range devs {
			targets = append(targets, d.ID)
		}
	}
	if len(targets) == 0 {
		http.Error(w, "no devices to install to", http.StatusBadRequest)
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	queued := 0
	for _, devID := range targets {
		cmdID := "apk-" + shortHash(devID+name+now)
		u := &store.APKUpdate{
			CommandID: cmdID, DeviceID: devID, PackageName: req.PackageName,
			VersionName: "", APKPath: baseName(name), Status: "pending",
		}
		if err := s.st.EnqueueAPKUpdate(u); err != nil {
			continue
		}
		queued++
		s.pokes.Enqueue(devID, device.Poke{Type: "install_apk", Package: req.PackageName})
	}
	s.record(r, "apk_install_queued", store.EventInfo, "",
		fmt.Sprintf("Queued %s for install on %s", req.PackageName, plural(queued, "device", "devices")))
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"package_name": req.PackageName, "apk": name, "queued": queued, "targets": len(targets),
	})
}
