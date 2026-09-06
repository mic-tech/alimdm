package httpapi

// What a tablet actually has installed.
//
// The policy says which apps a group should run; this says what is really on
// one device. The two drift, and the gap is where the awkward problems live: an
// OEM package whose name is not the one the policy guessed, an app installed
// before enrollment, one left behind after being dropped from the whitelist and
// still occupying a gigabyte.
//
// Shaped like the file inbox, and for the same reason: the tablet is behind NAT
// so the console cannot ask and wait. It queues a request, the device answers on
// its next check-in, and the answer is kept with the time it was taken. A stale
// listing that says when it was taken beats no listing.

import (
	"encoding/json"
	"net/http"
	"strings"

	"ali-mdm/server/internal/store"
)

// maxReportedApps bounds what one device can push into the database. A tablet
// with a few hundred packages is normal; ten thousand is not, and the row is
// read back whole on every view.
const maxReportedApps = 600

// deviceApps serves the cached inventory.
func (s *Server) deviceApps(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.st.GetDevice(id); err != nil {
		writeErr(w, http.StatusNotFound, "unknown device")
		return
	}
	entriesJSON, fetchedAt := s.st.DeviceApps(id)
	var entries []map[string]any
	if json.Unmarshal([]byte(entriesJSON), &entries) != nil || entries == nil {
		entries = []map[string]any{}
	}
	writeJSON(w, map[string]any{
		"device_id": id,
		"entries":   entries,
		// Empty until the tablet has answered once, which the console shows as
		// "never checked" rather than pretending nothing is installed.
		"fetched_at": fetchedAt,
	})
}

// refreshDeviceApps asks a tablet what it has installed.
func (s *Server) refreshDeviceApps(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.st.GetDevice(id); err != nil {
		writeErr(w, http.StatusNotFound, "unknown device")
		return
	}
	if err := s.queueDeviceCommand(id, "list_apps", nil); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not queue the request")
		return
	}
	writeJSON(w, map[string]any{"queued": true})
}

// uninstallDeviceApp asks a tablet to remove one package.
func (s *Server) uninstallDeviceApp(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	pkg := strings.TrimSpace(r.PathValue("package"))
	dev, err := s.st.GetDevice(id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "unknown device")
		return
	}
	if pkg == "" {
		writeErr(w, http.StatusBadRequest, "package name is required")
		return
	}
	// The one package that must never be uninstallable from here. Removing the
	// agent from a Device Owner tablet does not just end management: nothing is
	// left that could enroll it again, and the tablet has to be factory reset in
	// person. A confirmation dialog is not enough protection for that.
	if pkg == agentPackageName {
		writeErr(w, http.StatusBadRequest,
			"Ali MDM cannot uninstall itself — the tablet would need a factory reset to come back")
		return
	}
	if err := s.queueDeviceCommand(id, "uninstall_app", map[string]any{"package": pkg}); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not queue the uninstall")
		return
	}
	s.record(r, "app_uninstall_queued", store.EventWarn, id,
		"Asked "+deviceLabel(dev.ID, dev.Name)+" to uninstall "+pkg)
	writeJSON(w, map[string]any{"queued": true, "package": pkg})
}

// reportDeviceApps records what a tablet says it has installed.
func (s *Server) reportDeviceApps(w http.ResponseWriter, r *http.Request) {
	dev := deviceFrom(r.Context())
	if dev == nil {
		http.Error(w, "unknown device", http.StatusNotFound)
		return
	}
	var req struct {
		Entries []map[string]any `json:"entries"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	if req.Entries == nil {
		req.Entries = []map[string]any{}
	}
	if len(req.Entries) > maxReportedApps {
		req.Entries = req.Entries[:maxReportedApps]
	}
	body, err := json.Marshal(req.Entries)
	if err != nil {
		http.Error(w, "bad entries", http.StatusBadRequest)
		return
	}
	if err := s.st.SetDeviceApps(dev.ID, string(body)); err != nil {
		http.Error(w, "could not store the inventory", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"stored": len(req.Entries)})
}
