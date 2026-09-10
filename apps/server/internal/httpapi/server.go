package httpapi

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"ali-mdm/server/internal/apk"
	"ali-mdm/server/internal/apkinfo"
	"ali-mdm/server/internal/auth"
	"ali-mdm/server/internal/blob"
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
	agentAPKs *apk.Store
	// files holds operator-uploaded documents pushed to device inboxes, kept in
	// its own root so a worksheet can never be mistaken for an installable APK.
	files *blob.Store
	pokes *PokeQueue
	// streams fans a tablet's live-view frames out to console viewers. In
	// memory only: a picture of a classroom has no business on disk.
	streams *streamHubs
	// snapshots holds the latest still per device for the console's card view.
	// In memory only, like the live-view frames.
	snapshots   *snapshotStore
	enrollToken string
	logins      *loginGuard
	baseURL     string
	consoleDir  string
}

func New(st *store.Store, signer *auth.Signer, apks, agentAPKs *apk.Store, files *blob.Store, pokes *PokeQueue, enrollToken, baseURL, consoleDir string) *Server {
	return &Server{logins: newLoginGuard(), st: st, signer: signer, apks: apks, agentAPKs: agentAPKs, files: files, pokes: pokes, streams: newStreamHubs(), snapshots: newSnapshotStore(), enrollToken: enrollToken, baseURL: baseURL, consoleDir: consoleDir}
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
		// A password change ends the sessions the old password opened. Without
		// this, "I think someone has my token, I have changed my password" did
		// nothing for up to twelve hours.
		if op.PasswordChangedAt > 0 && claims.Iat > 0 && claims.Iat < op.PasswordChangedAt {
			http.Error(w, "session ended by a password change", http.StatusUnauthorized)
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

// Routes builds the router, wrapped in the response headers every deployment
// should have regardless of what sits in front of it.
func (s *Server) Routes() http.Handler {
	return withSecurityHeaders(s.routes())
}

func (s *Server) routes() *http.ServeMux {
	mux := http.NewServeMux()
	// Ali MDM device protocol
	// All /api/v1/devices/... routes go through one dispatcher to avoid Go 1.22 mux
	// pattern ambiguity between the literal "enroll" and the "{id}" segment.
	mux.HandleFunc("/api/v1/devices/", s.deviceDispatcher)
	// Device-authenticated: this writes the outcome of a queued command, and
	// until now anyone who could name a command id could write it. The tablets
	// have always sent their key here; only the server was not looking.
	mux.HandleFunc("POST /api/v1/commands/{id}/result/", s.requireDevice(s.commandResult))
	mux.HandleFunc("POST /api/v1/devices/{id}/screenshot/", s.requireDevice(s.screenshot))
	mux.HandleFunc("POST /api/v1/devices/{id}/unenroll/", s.unenroll)
	// Device-authenticated. These serve whatever has been uploaded to the
	// package library, under names that are simply the package id — so open,
	// they let anyone who guesses a name pull the fleet's software down. The
	// installer has always sent the device key with these requests.
	//
	// /provision/apk and /agent/apk below stay open on purpose and are not the
	// same case: they serve the Ali MDM build itself, which a factory-fresh
	// tablet must fetch before it has any key at all.
	mux.HandleFunc("GET /api/v1/apk/{name}", s.requireDevice(s.downloadAPK))
	mux.HandleFunc("GET /api/v1/apk/{name}/{part}", s.requireDevice(s.downloadAPKPart))
	// Zero-touch provisioning: the tablet's setup wizard downloads the Ali MDM
	// APK from here while scanning the QR. Open (no auth) — it runs before the
	// device has an API key. 404 until a build is staged on the App update page.
	mux.HandleFunc("GET /api/v1/provision/apk", s.provisionAPKHandler)
	// QR payload for setup-wizard provisioning (operator-only: it carries the
	// enrolment token).
	mux.HandleFunc("GET /api/v1/provision/qr", s.requireOperator(s.provisionQR))
	// Operator console
	mux.HandleFunc("POST /api/v1/operator/login", s.operatorLogin)
	mux.HandleFunc("GET /api/v1/devices", s.requireOperator(s.listDevices))
	// One device, in full — everything its own page shows above the history.
	// Registered before the device-protocol dispatcher's subtree can claim it;
	// a one-segment pattern is the more specific of the two.
	mux.HandleFunc("GET /api/v1/devices/{id}", s.requireOperator(s.deviceDetail))
	mux.HandleFunc("GET /api/v1/devices/{id}/commands", s.requireOperator(s.listDeviceCommands))
	// Diagnostics. Admin-only: a device log is a minute-by-minute account of a
	// classroom's tablet, which belongs with whoever runs the fleet.
	mux.HandleFunc("POST /api/v1/devices/{id}/logs", s.requireAdmin(s.requestDeviceLogs))
	mux.HandleFunc("GET /api/v1/devices/{id}/logs", s.requireAdmin(s.deviceLogs))
	mux.HandleFunc("POST /api/v1/devices/{id}/commands", s.requireOperator(s.enqueueCommand))
	mux.HandleFunc("GET /api/v1/groups", s.requireOperator(s.listGroups))
	mux.HandleFunc("POST /api/v1/groups", s.requireOperator(s.createGroup))
	mux.HandleFunc("GET /api/v1/groups/{id}", s.requireOperator(s.getGroup))
	mux.HandleFunc("PUT /api/v1/groups/{id}", s.requireOperator(s.updateGroup))
	mux.HandleFunc("DELETE /api/v1/groups/{id}", s.requireOperator(s.deleteGroup))
	mux.HandleFunc("POST /api/v1/devices/{id}/group", s.requireOperator(s.moveDeviceGroup))
	// Re-deliver the policy: to one device, or to a whole group at once.
	mux.HandleFunc("POST /api/v1/devices/{id}/config/resend", s.requireOperator(s.resendDeviceConfig))
	mux.HandleFunc("POST /api/v1/groups/{id}/config/resend", s.requireOperator(s.resendGroupConfig))
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
	// Folder-level routes take the folder in the body (push) or a query
	// parameter (delete) rather than the path: it contains slashes, which a
	// ServeMux wildcard would swallow the rest of the route to hold.
	mux.HandleFunc("POST /api/v1/folders/push", s.requireOperator(s.pushFolder))
	mux.HandleFunc("DELETE /api/v1/folders", s.requireOperator(s.deleteFolder))
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
	// Control: taps, Back/Home and text, delivered on the frame channel.
	mux.HandleFunc("POST /api/v1/devices/{id}/input", s.requireOperator(s.sendInput))

	// Snapshots: a still per device, for the console's card view.
	mux.HandleFunc("GET /api/v1/devices/{id}/snapshot", s.requireOperator(s.deviceSnapshot))
	mux.HandleFunc("GET /api/v1/devices/{id}/snapshot/meta", s.requireOperator(s.snapshotMeta))
	mux.HandleFunc("POST /api/v1/devices/{id}/snapshot/request", s.requireOperator(s.requestSnapshot))

	// Per-device file manager: the console asks, the tablet answers later.
	mux.HandleFunc("GET /api/v1/devices/{id}/inbox", s.requireOperator(s.deviceInbox))
	mux.HandleFunc("POST /api/v1/devices/{id}/inbox/refresh", s.requireOperator(s.refreshDeviceInbox))
	mux.HandleFunc("DELETE /api/v1/devices/{id}/inbox/{name}", s.requireOperator(s.deleteDeviceFile))
	mux.HandleFunc("POST /api/v1/devices/{id}/inbox", s.requireDevice(s.reportDeviceInbox))

	mux.HandleFunc("GET /api/v1/devices/{id}/apps", s.requireOperator(s.deviceApps))
	mux.HandleFunc("POST /api/v1/devices/{id}/apps/refresh", s.requireOperator(s.refreshDeviceApps))
	mux.HandleFunc("DELETE /api/v1/devices/{id}/apps/{package}", s.requireOperator(s.uninstallDeviceApp))
	mux.HandleFunc("POST /api/v1/devices/{id}/apps", s.requireDevice(s.reportDeviceApps))

	// The wake stream. Held open by the device; the server writes a line when
	// work is queued, and the device heartbeats in response.
	mux.HandleFunc("GET /api/v1/devices/{id}/events", s.requireDevice(s.deviceEvents))

	// The notification feed is readable by any signed-in operator: it is how
	// they see what the fleet and their colleagues have been doing.
	mux.HandleFunc("GET /api/v1/events", s.requireOperator(s.listEvents))
	mux.HandleFunc("POST /api/v1/events/read", s.requireOperator(s.markEventsRead))
	// Admin-only: the feed is the record of who did what, so clearing it is not
	// something an operator does to their own tracks.
	mux.HandleFunc("DELETE /api/v1/events", s.requireAdmin(s.clearEvents))

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
	if subtle.ConstantTimeCompare([]byte(req.Token), []byte(s.enrollToken)) != 1 {
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
	// Say what the tablet actually asked for, not just where it ended up.
	// A QR carries the group and label the operator chose, and until now a
	// tablet that arrived without them looked exactly like one that arrived
	// with them and was ignored: both read "Enrolled X into default". That is
	// the difference between a QR generated without a group and an extra lost
	// on the way, and it is the one thing the feed could not answer.
	sev := store.EventInfo
	summary := "Enrolled " + id
	if label != "" {
		summary += " as \u201c" + label + "\u201d"
	}
	summary += " into " + groupID
	switch {
	case req.GroupID != "" && req.GroupID != groupID:
		sev = store.EventWarn
		summary += " \u2014 it asked for group \u201c" + req.GroupID + "\u201d, which does not exist"
	case req.GroupID == "" && label == "":
		summary += " \u2014 it asked for no group and no label"
	}
	s.recordAs("device", "device_enrolled", sev, id, summary)
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
	_ = s.st.UpdateHeartbeat(dev.ID, group.ConfigHash, req.Battery.Level, 0, req.Battery.Charging,
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
	// Pokes are a wake signal and nothing more. Every one of them accompanies
	// something the device finds by itself: a command already sitting in the
	// queue, an APK update in /updates/, or a flag in this very response
	// (stream_requested, pending_files).
	//
	// This used to turn each drained poke back into a command, which ran every
	// console command on the device twice — once correctly, and once from a
	// poke that carries no parameters. On the fleet that showed up as pairs of
	// rows: "launch_app success" beside "launch_app error: Missing package
	// name", and manufactured rows for types the app has no handler for,
	// settling as "Unsupported command type: start_stream".
	//
	// Still drained, so the queue does not grow for a device with no MQTT.
	s.pokes.Drain(dev.ID)
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
		name := baseName(u.APKPath)
		item := map[string]any{
			"command_id":   u.CommandID,
			"package_name": u.PackageName,
			"version_name": u.VersionName,
			"download_url": s.baseURL + "/api/v1/apk/" + name,
		}
		// A split package installs as a set or not at all, so the device is
		// given every part. download_url still points at the base: a build that
		// predates split support then fails on a missing split, which is a
		// clearer outcome than quietly installing a base whose native libraries
		// are in a split it never fetched.
		if parts := s.apks.PartsOf(name); parts != nil {
			urls := make([]string, 0, len(parts))
			for _, part := range parts {
				urls = append(urls, s.baseURL+"/api/v1/apk/"+name+"/"+part)
			}
			item["split_urls"] = urls
			item["download_url"] = urls[0] // the base, which PartsOf puts first
		}
		out = append(out, item)
	}
	// Never cached. This hands work over and marks it claimed in the same
	// breath, so a 304 would mean the installs were recorded as sent and never
	// delivered. Only the console's read-only lists carry validators.
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

// reasonSuffix appends the device's own words when it gave any, and translates
// the one failure that is not about the package at all.
//
// INSTALL_FAILED_VERIFICATION_FAILURE is Play Protect on the tablet refusing an
// app it does not recognise — which for a fleet's own in-house builds is every
// one of them, on every newly enrolled device. Left as the raw string it reads
// like a corrupt download, and the operator goes looking at the APK. It is not
// the APK: the same file installs on tablets that have already accepted it.
func reasonSuffix(reason string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return " (the device gave no reason)"
	}
	hint := ""
	if strings.Contains(reason, "INSTALL_FAILED_VERIFICATION_FAILURE") {
		hint = " \u2014 Play Protect on the tablet refused it, which it does for apps it does not " +
			"recognise. Nothing is wrong with the package. Approve it once on the device, or turn " +
			"off Play Store \u203a Play Protect \u203a Scan apps."
	}
	if len(reason) > 300 {
		reason = reason[:300] + "\u2026"
	}
	return ": " + reason + hint
}

func (s *Server) commandResult(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		Status       string          `json:"status"`
		Result       json.RawMessage `json:"result"`
		ErrorMessage string          `json:"error_message"`
	}
	// A log is the first result with any size to it; unbounded, one device
	// could fill the database with them.
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, maxCommandResultBytes)).Decode(&req) != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	dev := deviceFrom(r.Context())
	if dev == nil {
		http.Error(w, "unknown device", http.StatusUnauthorized)
		return
	}
	err := s.st.ReportCommandResult(id, dev.ID, req.Status, string(req.Result), req.ErrorMessage)
	var failed *store.InstallFailed
	switch {
	case errors.As(err, &failed):
		// A failed install used to be invisible: the row said "failed" at best,
		// nothing said why, and the console showed an app that simply never
		// appeared. Put it where an operator will see it.
		s.recordAs("device", "app_install_failed", store.EventError, dev.ID,
			"Could not install "+failed.Package+" on "+deviceLabel(dev.ID, dev.Name)+
				reasonSuffix(failed.Reason))
	case err != nil:
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
	_ = s.st.UpdateHeartbeat(dev.ID, "", 0, 0, false, "", "", "")
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
	dev, err := s.st.GetDevice(id)
	if err != nil {
		http.Error(w, "unknown device", http.StatusNotFound)
		return
	}
	// Read before the row goes: afterwards there is nothing left to name it by.
	removedLabel := deviceLabel(dev.ID, dev.Name)
	if err := s.st.DeleteDevice(id); err != nil {
		http.Error(w, "unenroll failed", http.StatusInternalServerError)
		return
	}
	// Forget its picture too; the row is gone, the image should go with it.
	s.snapshots.drop(id)
	s.record(r, "device_removed", store.EventWarn, id,
		"Removed "+removedLabel+" from the console")
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

// downloadAPKPart serves one APK out of a split package.
//
// Both segments are single path components, so the route needs no wildcard
// gymnastics — a split package is stored under its package name, and its parts
// under theirs.
func (s *Server) downloadAPKPart(w http.ResponseWriter, r *http.Request) {
	pkg := baseName(r.PathValue("name"))
	part := baseName(r.PathValue("part"))
	f, err := s.apks.OpenPart(pkg, part)
	if err != nil {
		http.Error(w, "apk not found", http.StatusNotFound)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/vnd.android.package-archive")
	w.Header().Set("Content-Disposition", `attachment; filename="`+part+`"`)
	io.Copy(w, f)
}

// openProvisionAPK opens the build a QR-provisioned tablet will install: the
// release staged on the App update page, and nothing else.
//
// There used to be a second source — a path in ALIMDM_PROVISION_APK, pointing at a
// file someone copied onto the server by hand. Nobody remembered it. The console
// rolled build 65 to the fleet for a day while every newly provisioned tablet
// installed a four-day-old APK from that file, and nothing anywhere said so.
// A default that is silently wrong is worse than no default: with one source,
// forgetting to upload a build is an error on the Enroll page rather than a
// tablet that quietly arrives out of date.
func (s *Server) openProvisionAPK() (io.ReadCloser, string, error) {
	if s.agentAPKs == nil {
		return nil, "", errors.New("no agent apk store")
	}
	rel, err := s.st.GetAgentRelease()
	if err != nil || rel.FileName == "" {
		return nil, "", errors.New("no build staged")
	}
	f, err := s.agentAPKs.Open(rel.FileName)
	if err != nil {
		return nil, "", err
	}
	return f, rel.FileName, nil
}

// provisionAPKHandler serves the Ali MDM APK for zero-touch QR provisioning.
// The Android setup wizard fetches this URL (from the QR's
// PROVISIONING_DEVICE_ADMIN_PACKAGE_DOWNLOAD_LOCATION) and installs it as the
// Device Owner. It must be open (no auth) because it runs before the device is
// enrolled.
func (s *Server) provisionAPKHandler(w http.ResponseWriter, r *http.Request) {
	f, _, err := s.openProvisionAPK()
	if err != nil {
		http.Error(w, "no build has been staged: upload one on the App update page",
			http.StatusNotFound)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/vnd.android.package-archive")
	w.Header().Set("Content-Disposition", `attachment; filename="alimdm.apk"`)
	io.Copy(w, f)
}

// ── Operator console ─────────────────────────────────────────────────────────

// dummyPasswordHash is verified against when no account matches, so a miss and
// a hit take the same time. Derived from random bytes at startup: no password
// verifies against it, and it is never compared to anything a user typed.
var dummyPasswordHash = func() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	h, err := auth.HashPassword(hex.EncodeToString(b))
	if err != nil {
		return ""
	}
	return h
}()

func (s *Server) operatorLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	if locked, wait := s.logins.locked(req.Email); locked {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
		http.Error(w, "too many failed attempts — try again shortly", http.StatusTooManyRequests)
		return
	}
	op, err := s.st.GetOperator(req.Email)
	// An unknown email used to return before any hashing happened, so it
	// answered in microseconds where a real one took ~50ms of PBKDF2. That
	// difference is a reliable oracle for which addresses have accounts, so a
	// miss now does the same work against a throwaway hash.
	hash := dummyPasswordHash
	if err == nil {
		hash = op.PasswordHash
	}
	if !auth.VerifyPassword(req.Password, hash) || err != nil {
		s.logins.fails(req.Email)
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}
	s.logins.succeeds(req.Email)
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
		// Only meaningful while the device is checking in: a tablet that went
		// offline on charge keeps the last value it sent.
		Charging      bool   `json:"charging"`
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
		// Whether this device is holding the wake stream open, and so whether a
		// command reaches it in a second or at its next check-in. In the list as
		// well as on the device page: across a fleet, the useful question is
		// which tablets are on the fast path, not whether one is.
		WakeStream bool `json:"wake_stream"`
	}
	// The staged release is what every tablet is expected to converge on.
	staged := 0
	if rel, err := s.st.GetAgentRelease(); err == nil {
		staged = rel.VersionCode
	}
	// Policy versions come from the groups, not from the devices' own unmaintained
	// column. One lookup each rather than one per device.
	groupVersion := map[string]int{}
	if groups, err := s.st.ListGroups(); err == nil {
		for _, g := range groups {
			groupVersion[g.ID] = g.ConfigVersion
		}
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
		out = append(out, pub{d.ID, d.Name, d.GroupID, groupVersion[d.GroupID], d.Battery, d.Charging, d.AndroidVer, d.Model,
			d.AppVersionCode, d.AppVersionName, stale, d.LastSeen, online, s.pokes.Waiting(d.ID) > 0})
	}
	writeJSONCached(w, r, out)
}

// deviceDetail is one device's full record, for its own page.
//
// Deliberately not the list row plus extras: the page shows things the list has
// no room for — when it enrolled, which config version it is on, what is still
// queued for it — and computing them here keeps the console from having to
// stitch three responses together to describe one device.
func (s *Server) deviceDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	d, err := s.st.GetDevice(id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "unknown device")
		return
	}

	online := false
	if t, err := time.Parse(time.RFC3339, d.LastSeen); err == nil {
		online = time.Since(t) < 3*time.Minute
	}

	// What the fleet is expected to converge on, so the page can say "behind"
	// rather than leaving an operator to compare two version numbers by eye.
	staged, stagedName := 0, ""
	if rel, err := s.st.GetAgentRelease(); err == nil {
		staged, stagedName = rel.VersionCode, rel.VersionName
	}

	// The policy version a device is on is the version of the group it is in.
	// The devices table has a config_version column of its own, written once at
	// enrolment and never again — every device page in the console read it and
	// said "v0" while the fleet was on v19.
	groupName := d.GroupID
	configVersion := 0
	configPending := false
	if g, err := s.st.GetGroup(d.GroupID); err == nil {
		groupName = g.Name
		configVersion = g.ConfigVersion
		// The hash is what the device was last handed, so this says the current
		// policy has not reached it yet — not that it failed to apply it.
		configPending = g.ConfigHash != d.LastAppliedHash
	}

	// Only what is still waiting. A finished command is history, and the
	// history is the feed below it on the page.
	pending := 0
	if cmds, err := s.st.ListCommands(d.ID); err == nil {
		for _, c := range cmds {
			if c.Status == "pending" || c.Status == "sent" {
				pending++
			}
		}
	}

	writeJSON(w, map[string]any{
		"id":               d.ID,
		"name":             d.Name,
		"label":            deviceLabel(d.ID, d.Name),
		"group_id":         d.GroupID,
		"group_name":       groupName,
		"config_version":   configVersion,
		"config_pending":   configPending,
		"battery":          d.Battery,
		"charging":         d.Charging,
		"android_ver":      d.AndroidVer,
		"model":            d.Model,
		"app_version_code": d.AppVersionCode,
		"app_version_name": d.AppVersionName,
		"staged_version":   stagedName,
		"stale":            staged > 0 && d.AppVersionCode < staged,
		"last_seen":        d.LastSeen,
		"online":           online,
		"enrolled_at":      d.CreatedAt,
		"pending_commands": pending,
		// Whether this device is holding the wake stream open, and so whether a
		// command will land in a second or at its next check-in. Without this
		// the feature is invisible: the only way to tell it is working is to
		// queue something and watch a clock.
		"wake_stream": s.pokes.Waiting(d.ID) > 0,
	})
}

// maxCommandResultBytes bounds what a device may post back as a command result.
const maxCommandResultBytes = 1 << 20

// resendDeviceConfig has one device treated as out of date, so its next
// check-in is answered with the policy in full.
//
// There is no push: the tablets are behind school NAT and the config travels on
// the heartbeat they make every 30 seconds. What this does is make the server
// stop assuming the device already has it — which it assumes for good after the
// first delivery, and which is wrong the moment anything on the tablet drifts.
func (s *Server) resendDeviceConfig(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	dev, err := s.st.GetDevice(id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "unknown device")
		return
	}
	if err := s.st.ResendConfig(dev.ID); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not queue the policy")
		return
	}
	s.record(r, "config_resent", store.EventInfo, dev.ID,
		"Re-sending the policy to "+deviceLabel(dev.ID, dev.Name))
	writeJSON(w, map[string]any{"resent": 1})
}

// resendGroupConfig does the same for every device in a group.
func (s *Server) resendGroupConfig(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	g, err := s.st.GetGroup(id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "unknown group")
		return
	}
	n, err := s.st.ResendConfigToGroup(g.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not queue the policy")
		return
	}
	s.record(r, "config_resent", store.EventInfo, "",
		fmt.Sprintf("Re-sending the %s policy to %s", g.Name, plural(n, "device", "devices")))
	writeJSON(w, map[string]any{"resent": n})
}

// singleTarget names the device an action was aimed at, when it was aimed at
// exactly one. Empty for a fleet-wide action, which has no single device to
// attribute it to.
func singleTarget(devices []string) string {
	if len(devices) == 1 {
		return devices[0]
	}
	return ""
}

// listDeviceCommands is what has been queued for one device, and how it went.
//
// The store's struct carries no JSON tags, so encoding it directly emitted Go
// field names — ID, CreatedAt, ErrMsg — while every other endpoint here speaks
// snake_case. Nothing read this route until the device page did, and then read
// nothing but empty cells.
func (s *Server) listDeviceCommands(w http.ResponseWriter, r *http.Request) {
	cmds, _ := s.st.ListCommands(r.PathValue("id"))
	out := make([]map[string]any, 0, len(cmds))
	for _, c := range cmds {
		out = append(out, map[string]any{
			"id":         c.ID,
			"device_id":  c.DeviceID,
			"type":       c.Type,
			"status":     c.Status,
			"err_msg":    c.ErrMsg,
			"created_at": c.CreatedAt,
			"expires_at": c.ExpiresAt,
		})
	}
	writeJSON(w, out)
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
	label := id
	if dev, err := s.st.GetDevice(id); err == nil {
		label = deviceLabel(dev.ID, dev.Name)
	}
	s.record(r, "command_sent", store.EventInfo, id, "Sent "+req.Type+" to "+label)
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
				_ = s.st.UpdateHeartbeat(d.ID, "", d.Battery, 0, d.Charging, d.AndroidVer, d.Model, d.LastSeen)
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
	dev, err := s.st.GetDevice(id)
	if err != nil {
		http.Error(w, "unknown device", http.StatusNotFound)
		return
	}
	// Read before the change: the entry is about what it was called until now.
	was := deviceLabel(dev.ID, dev.Name)
	if err := s.st.SetDeviceName(id, name); err != nil {
		http.Error(w, "rename failed", http.StatusInternalServerError)
		return
	}
	if name == "" {
		s.record(r, "device_renamed", store.EventInfo, id, "Cleared the label on "+was)
	} else {
		s.record(r, "device_renamed", store.EventInfo, id, "Renamed "+was+" to \u201c"+name+"\u201d")
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"id": id, "name": name})
}

func (s *Server) uploadAPK(w http.ResponseWriter, r *http.Request) {
	// 16MB, not 512MB. This is how much of the upload Go holds in memory before
	// spilling the rest to a temp file, and a split archive is routinely
	// hundreds of megabytes — a real one seen here was 732MB. At the old figure
	// a single upload could take half a gigabyte of RAM on a server with two
	// available and no swap. Everything past this lands on disk, which is where
	// it was going anyway.
	if err := r.ParseMultipartForm(16 << 20); err != nil {
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
	if apk.IsArchiveName(name) {
		s.uploadSplitAPK(w, r, file, name)
		return
	}
	sha, path, size, err := s.apks.Ingest(file, name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// Read what the APK says it is. The file name is whatever the download was
	// called; the package name is what an install actually targets, and asking
	// an operator to type it is a step that can go wrong in a way nothing
	// catches until the wrong app is installed. A file that will not parse is
	// still stored — it may be a perfectly good APK this parser cannot read —
	// and simply leaves the field empty.
	info, infoErr := apkinfo.ReadAPK(path)
	if infoErr != nil {
		s.record(r, "apk_uploaded", store.EventWarn, "",
			"Uploaded "+baseName(path)+", but could not read its manifest: "+infoErr.Error())
	}
	_ = s.st.SaveAPK(&store.APK{
		Name: baseName(path), SHA256: sha, Size: size, Path: path,
		PackageName: info.PackageName, VersionName: info.VersionName,
	})
	if infoErr == nil {
		s.record(r, "apk_uploaded", store.EventInfo, "",
			fmt.Sprintf("Uploaded package %s (%s %s)", baseName(path), info.PackageName, info.VersionName))
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"name": baseName(path), "sha256": sha, "size": size,
		"package_name": info.PackageName, "version_name": info.VersionName,
	})
}

// uploadSplitAPK stores a .xapk/.apks archive as the set of APKs it holds.
//
// The archive is unpacked here rather than on the tablet for two reasons. The
// package name has to be read out of the base APK's manifest anyway — it is what
// the catalogue is keyed on and what enrollment matches a managed app against —
// so the archive must be opened server-side regardless. And a malformed archive
// then fails once, in front of the operator who chose it, instead of on every
// tablet in the school at the next check-in.
//
// The stored name is the package name, not the uploaded file name: the operator
// is not the one who should have to know that "Instagram_v300.xapk" has to be
// called com.instagram.android for enrollment to match it up.
func (s *Server) uploadSplitAPK(w http.ResponseWriter, r *http.Request, file io.Reader, name string) {
	set, err := s.apks.UnpackArchive(file)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// Anything that fails from here leaves an unpacked directory behind, so
	// clear it unless the upload finishes.
	adopted := false
	defer func() {
		if !adopted {
			os.RemoveAll(set.Dir)
		}
	}()

	info, err := apkinfo.ReadAPK(filepath.Join(set.Dir, set.Base))
	if err != nil {
		http.Error(w, "cannot read the base APK in that archive: "+err.Error(), http.StatusBadRequest)
		return
	}
	if info.PackageName == "" {
		http.Error(w, "the base APK in that archive names no package", http.StatusBadRequest)
		return
	}

	stored := info.PackageName + ".xapk"
	dir, err := s.apks.AdoptSplitSet(set, stored)
	if err != nil {
		http.Error(w, "could not store the package", http.StatusInternalServerError)
		return
	}
	adopted = true

	var size int64
	for _, part := range set.Parts {
		if st, err := os.Stat(filepath.Join(dir, part)); err == nil {
			size += st.Size()
		}
	}
	// No single sha256 to give: the thing installed is a set, not a file. The
	// parts are re-read from disk at install time anyway.
	_ = s.st.SaveAPK(&store.APK{
		Name: stored, SHA256: "", Size: size, Path: dir,
		PackageName: info.PackageName, VersionName: info.VersionName,
	})
	s.record(r, "apk_uploaded", store.EventInfo, "",
		fmt.Sprintf("Uploaded %s as %s (%d APKs, base %s)",
			name, stored, len(set.Parts), set.Base))
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"name": stored, "sha256": "", "size": size,
		"package_name": info.PackageName, "version_name": info.VersionName,
		"parts": set.Parts,
	})
}

func (s *Server) listAPKs(w http.ResponseWriter, r *http.Request) {
	apks, _ := s.st.ListAPKs()
	out := make([]map[string]any, 0, len(apks))
	for _, a := range apks {
		// Packages uploaded before the manifest was read have no package name.
		// Read it now and keep it, so the cost is paid once rather than on every
		// listing — and so an operator who uploaded before this existed still
		// gets the field filled in for them.
		if a.PackageName == "" {
			if info, err := apkinfo.ReadAPK(s.apkPathFor(a)); err == nil && info.PackageName != "" {
				a.PackageName, a.VersionName = info.PackageName, info.VersionName
				_ = s.st.SaveAPK(&a)
			}
		}
		item := map[string]any{
			"name": a.Name, "sha256": a.SHA256, "size": a.Size,
			"package_name": a.PackageName, "version_name": a.VersionName,
		}
		// A split package has no single sha256 to show — it is a set of APKs,
		// not a file — so say how many it holds instead of showing an empty
		// hash where every other row has one.
		if parts := s.apks.PartsOf(a.Name); parts != nil {
			item["parts"] = parts
		}
		out = append(out, item)
	}
	writeJSONCached(w, r, out)
}

// apkPathFor names the file whose manifest describes a catalogue entry: the APK
// itself, or a split package's base.
func (s *Server) apkPathFor(a store.APK) string {
	if parts := s.apks.PartsOf(a.Name); parts != nil {
		return filepath.Join(a.Path, parts[0])
	}
	return a.Path
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
	name := baseName(r.PathValue("name"))
	// A split package is a directory of parts, not a file. os.Open happens to
	// succeed on a directory, so the old check passed it by luck rather than by
	// intent; say what is actually being looked for.
	if s.apks.PartsOf(name) == nil {
		if _, err := s.apks.Open(name); err != nil {
			http.Error(w, "apk not found", http.StatusNotFound)
			return
		}
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
	// An install aimed at one device belongs in that device's history. A
	// fleet-wide push stays a fleet event rather than being repeated on all
	// twelve.
	s.record(r, "apk_install_queued", store.EventInfo, singleTarget(req.Devices),
		fmt.Sprintf("Queued %s for install on %s", req.PackageName, plural(queued, "device", "devices")))
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"package_name": req.PackageName, "apk": name, "queued": queued, "targets": len(targets),
	})
}
