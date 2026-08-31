package httpapi

// Agent (self) OTA — deliberately separate from the managed-app install path.
//
// Installing a managed app is fire-and-forget: the device commits a
// PackageInstaller session and reports the broadcast result. Installing the
// *agent* replaces com.alimdm while it is running, so Android kills the process
// mid-commit and that result broadcast never arrives. Reusing apk_updates would
// therefore leave every self-update stuck in "sent" forever, with no way to tell
// a success from a crash loop.
//
// This flow instead treats the kill as expected:
//   1. operator uploads a build and triggers a rollout (on-demand, per device)
//   2. device sees agent_update on its heartbeat, downloads, verifies sha256
//   3. device reports "installing" and persists its intent natively, then commits
//   4. process dies; on next launch the device compares its own versionCode to
//      the target and reports success or failure
//
// Every step is idempotent, so a device that dies at any point re-reports on the
// next heartbeat rather than stalling.

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"ali-mdm/server/internal/apkinfo"
	"ali-mdm/server/internal/store"
)

// maxAgentAttempts caps how many times a device may report "installing" for one
// rollout before the server stops offering it. Without this an update that
// reliably crashes on apply would loop forever, re-downloading each heartbeat.
const maxAgentAttempts = 3

// The agent is this app; anything else uploaded here could not replace it.
const agentPackageName = "com.alimdm"

// uploadAgentRelease stores a new Ali MDM build and makes it the current release.
// version_code is supplied by the operator: parsing it out of the APK would mean
// decoding binary AndroidManifest.xml, and getting it wrong silently breaks the
// "is this device already up to date" comparison.
func (s *Server) uploadAgentRelease(w http.ResponseWriter, r *http.Request) {
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

	name := hdr.Filename
	if !strings.HasSuffix(strings.ToLower(name), ".apk") {
		writeErr(w, http.StatusBadRequest, "file must be an .apk")
		return
	}
	sha, path, size, err := s.agentAPKs.Ingest(file, name)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not store the upload")
		return
	}

	// Read the version out of the APK rather than asking for it. A hand-typed
	// version that disagrees with the file is rejected by the device *after* it
	// has downloaded the whole build, which reads as a network fault; and it is
	// one more thing to get right on every release.
	info, err := apkinfo.ReadAPK(path)
	if err != nil {
		_ = s.agentAPKs.Delete(baseName(name))
		writeErr(w, http.StatusBadRequest, "could not read the APK's manifest: "+err.Error())
		return
	}
	// Uploading a different app as the agent would hand every tablet a build
	// that cannot replace the one it is running.
	if info.PackageName != agentPackageName {
		_ = s.agentAPKs.Delete(baseName(name))
		writeErr(w, http.StatusBadRequest,
			"that APK is "+info.PackageName+", not "+agentPackageName)
		return
	}
	if info.VersionCode <= 0 {
		_ = s.agentAPKs.Delete(baseName(name))
		writeErr(w, http.StatusBadRequest, "the APK declares no versionCode")
		return
	}

	rel := &store.AgentRelease{
		VersionCode: info.VersionCode, VersionName: info.VersionName,
		FileName: baseName(name), SHA256: sha, Size: size,
	}
	if err := s.st.SaveAgentRelease(rel); err != nil {
		http.Error(w, "save failed", http.StatusInternalServerError)
		return
	}
	writeJSON(w, rel)
}

// getAgentRelease returns the current build, or 404 when none is uploaded yet.
func (s *Server) getAgentRelease(w http.ResponseWriter, r *http.Request) {
	rel, err := s.st.GetAgentRelease()
	if err != nil {
		http.Error(w, "no agent release uploaded", http.StatusNotFound)
		return
	}
	writeJSON(w, rel)
}

// downloadAgentAPK serves the agent build to devices. It matches the existing
// managed-APK download route in being unauthenticated: the device fetches it
// with a plain HTTP client during an install session where no bearer token is
// available, and the payload is a signed APK the tablet verifies by hash.
func (s *Server) downloadAgentAPK(w http.ResponseWriter, r *http.Request) {
	rel, err := s.st.GetAgentRelease()
	if err != nil {
		http.Error(w, "no agent release", http.StatusNotFound)
		return
	}
	f, err := s.agentAPKs.Open(rel.FileName)
	if err != nil {
		http.Error(w, "agent apk missing", http.StatusNotFound)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/vnd.android.package-archive")
	// Zero mod-time: the device verifies the payload by sha256, so cache
	// validators would only add a way for a stale copy to be served.
	http.ServeContent(w, r, rel.FileName, time.Time{}, f)
}

// rolloutAgentUpdate arms the current release for the named devices, or for
// every enrolled device when the list is empty. Body:
//
//	{"devices": ["serial-1","serial-2"]}   // omit/empty = all
func (s *Server) rolloutAgentUpdate(w http.ResponseWriter, r *http.Request) {
	rel, err := s.st.GetAgentRelease()
	if err != nil {
		http.Error(w, "upload an agent build first", http.StatusBadRequest)
		return
	}
	var req struct {
		Devices []string `json:"devices"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	targets := req.Devices
	if len(targets) == 0 {
		devs, _ := s.st.ListDevices()
		for _, d := range devs {
			targets = append(targets, d.ID)
		}
	}
	queued := 0
	for _, id := range targets {
		if _, err := s.st.GetDevice(id); err != nil {
			continue // skip unknown ids rather than failing the whole rollout
		}
		if s.st.QueueAgentUpdate(id, rel.VersionCode) == nil {
			queued++
		}
	}
	writeJSON(w, map[string]any{"queued": queued, "targets": len(targets), "version_code": rel.VersionCode})
}

// listAgentUpdates powers the rollout table in the console.
func (s *Server) listAgentUpdates(w http.ResponseWriter, r *http.Request) {
	ups, _ := s.st.ListAgentUpdates()
	if ups == nil {
		ups = []store.AgentUpdate{}
	}
	writeJSON(w, ups)
}

// agentUpdateResult receives progress from a device. Called with the device's
// own API key, so a tablet can only ever report about itself.
//
//	{"status":"installing"|"success"|"failed", "error":"..."}
func (s *Server) agentUpdateResult(w http.ResponseWriter, r *http.Request) {
	dev := deviceFrom(r.Context())
	if dev == nil {
		http.Error(w, "unknown device", http.StatusNotFound)
		return
	}
	var req struct {
		Status string `json:"status"`
		Error  string `json:"error"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	switch req.Status {
	case store.AgentInstalling, store.AgentSuccess, store.AgentFailed:
	default:
		http.Error(w, "bad status", http.StatusBadRequest)
		return
	}
	if err := s.st.SetAgentUpdateStatus(dev.ID, req.Status, req.Error); err != nil {
		http.Error(w, "update failed", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// agentUpdateFor builds the heartbeat payload offering an update to this device,
// or nil when there is nothing to do. Returning nil for an exhausted or already
// finished rollout is what stops a crash-looping device from re-downloading the
// same broken build forever.
func (s *Server) agentUpdateFor(deviceID string) map[string]any {
	up, err := s.st.GetAgentUpdate(deviceID)
	if err != nil {
		return nil
	}
	if up.Status == store.AgentSuccess || up.Status == store.AgentFailed {
		return nil
	}
	if up.Attempts >= maxAgentAttempts {
		// Park it as failed so the console shows why it stopped retrying.
		_ = s.st.SetAgentUpdateStatus(deviceID, store.AgentFailed,
			"gave up after "+strconv.Itoa(up.Attempts)+" attempts")
		return nil
	}
	rel, err := s.st.GetAgentRelease()
	if err != nil || rel.VersionCode != up.TargetVersionCode {
		return nil // release was replaced under us; wait for a fresh rollout
	}
	return map[string]any{
		"version_code": rel.VersionCode,
		"version_name": rel.VersionName,
		"sha256":       rel.SHA256,
		"download_url": s.baseURL + "/api/v1/agent/apk",
		"attempt":      up.Attempts + 1,
		"max_attempts": maxAgentAttempts,
	}
}
