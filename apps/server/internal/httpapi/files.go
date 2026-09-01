package httpapi

// Console → device file inbox.
//
// The case this exists for: put a PDF on every tablet in a class at once.
// Doing that through a per-device file API means twelve uploads and twelve
// chances to miss one, so the unit of work here is the push, not the file:
// upload once, pick a group, and every tablet in it fetches the file on its
// next check-in into a shared inbox folder its own Files app can open.
//
// Delivery is tracked per device because a push that quietly missed half the
// fleet is worse than no push — nobody would think to go and check.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"ali-mdm/server/internal/device"
	"ali-mdm/server/internal/store"
)

// maxFileBytes bounds an upload. Large enough for the worksheets and slide
// decks this is for, small enough that one operator cannot fill the disk.
const maxFileBytes = 128 << 20

// uploadFile ingests a file into the catalogue. It does not push it anywhere:
// uploading and sending are separate so an operator can stage something without
// it appearing on twelve tablets the moment they pick the wrong menu item.
func (s *Server) uploadFile(w http.ResponseWriter, r *http.Request) {
	if s.files == nil {
		writeErr(w, http.StatusServiceUnavailable, "file storage is not configured")
		return
	}
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeErr(w, http.StatusBadRequest, "bad upload")
		return
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "missing file")
		return
	}
	defer file.Close()
	if hdr.Size > maxFileBytes {
		writeErr(w, http.StatusBadRequest,
			fmt.Sprintf("file is larger than the %d MB limit", maxFileBytes>>20))
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		name = hdr.Filename
	}
	sha, path, size, err := s.files.Ingest(file, name, maxFileBytes)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	f := &store.File{
		Name: baseName(path), SHA256: sha, Size: size,
		ContentType: hdr.Header.Get("Content-Type"), Path: path,
	}
	if err := s.st.SaveFile(f); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not save the file")
		return
	}
	s.record(r, "file_uploaded", store.EventInfo, "", "Uploaded "+f.Name+" to the file library")
	writeJSON(w, f)
}

func (s *Server) listFiles(w http.ResponseWriter, r *http.Request) {
	files, err := s.st.ListFiles()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not read the file library")
		return
	}
	// Each entry carries where its push got to, so the list itself answers
	// "did that reach the tablets?" without a second click.
	out := make([]map[string]any, 0, len(files))
	for _, f := range files {
		ds, _ := s.st.FileDeliveries(f.Name)
		done, failed, pending := 0, 0, 0
		for _, d := range ds {
			switch d.Status {
			case store.FileDeliveryDone:
				done++
			case store.FileDeliveryFailed:
				failed++
			default:
				pending++
			}
		}
		out = append(out, map[string]any{
			"name": f.Name, "sha256": f.SHA256, "size": f.Size,
			"content_type": f.ContentType, "uploaded_at": f.UploadedAt,
			"delivered": done, "failed": failed, "pending": pending,
			"targets": len(ds),
		})
	}
	writeJSON(w, out)
}

func (s *Server) deleteFile(w http.ResponseWriter, r *http.Request) {
	name := baseName(r.PathValue("name"))
	if _, err := s.st.GetFile(name); err != nil {
		writeErr(w, http.StatusNotFound, "no such file")
		return
	}
	// Row first: a catalogue entry pointing at a missing file is worse than an
	// orphaned file, which simply stops being listed.
	if err := s.st.DeleteFile(name); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not delete the file")
		return
	}
	if s.files != nil {
		_ = s.files.Delete(name)
	}
	s.record(r, "file_deleted", store.EventWarn, "",
		"Deleted "+name+" from the library (copies already on tablets are left alone)")
	writeJSON(w, map[string]any{"name": name, "deleted": true})
}

// pushFile queues a file for a set of devices, a whole group, or the fleet.
func (s *Server) pushFile(w http.ResponseWriter, r *http.Request) {
	name := baseName(r.PathValue("name"))
	if _, err := s.st.GetFile(name); err != nil {
		writeErr(w, http.StatusNotFound, "no such file")
		return
	}
	var req struct {
		Devices []string `json:"devices"`
		GroupID string   `json:"group_id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	targets := req.Devices
	if len(targets) == 0 {
		devs, err := s.st.ListDevices()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "could not list devices")
			return
		}
		for _, d := range devs {
			// An empty group_id means the whole fleet.
			if req.GroupID == "" || d.GroupID == req.GroupID {
				targets = append(targets, d.ID)
			}
		}
	}
	if len(targets) == 0 {
		writeErr(w, http.StatusBadRequest, "no devices to send to")
		return
	}

	queued := 0
	for _, id := range targets {
		if _, err := s.st.GetDevice(id); err != nil {
			continue // skip unknown ids rather than failing the whole push
		}
		if s.st.QueueFileDelivery(id, name) == nil {
			queued++
			// Wake the device so the file lands in seconds rather than at the
			// next heartbeat. Harmless when MQTT is not configured.
			s.pokes.Enqueue(id, device.Poke{Type: "fetch_files"})
		}
	}
	s.record(r, "file_pushed", store.EventInfo, "",
		fmt.Sprintf("Pushed %s to %s", name, plural(queued, "device", "devices")))
	writeJSON(w, map[string]any{"name": name, "queued": queued, "targets": len(targets)})
}

// fileDeliveries reports per-device delivery state for one file.
func (s *Server) fileDeliveries(w http.ResponseWriter, r *http.Request) {
	name := baseName(r.PathValue("name"))
	ds, err := s.st.FileDeliveries(name)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not read deliveries")
		return
	}
	writeJSON(w, ds)
}

// ── Device-facing ────────────────────────────────────────────────────────────

// devicePendingFiles hands a tablet the files it has not taken yet.
func (s *Server) devicePendingFiles(w http.ResponseWriter, r *http.Request) {
	dev := deviceFrom(r.Context())
	if dev == nil {
		http.Error(w, "unknown device", http.StatusNotFound)
		return
	}
	ds, _ := s.st.ClaimPendingFileDeliveries(dev.ID)
	out := make([]map[string]any, 0, len(ds))
	for _, d := range ds {
		f, err := s.st.GetFile(d.FileName)
		if err != nil {
			continue // catalogue entry went away between push and fetch
		}
		out = append(out, map[string]any{
			"name":         f.Name,
			"sha256":       f.SHA256,
			"size":         f.Size,
			"content_type": f.ContentType,
			"download_url": s.baseURL + "/api/v1/files/" + f.Name + "/download",
		})
	}
	writeJSON(w, out)
}

// downloadFile serves the bytes to a device.
func (s *Server) downloadFile(w http.ResponseWriter, r *http.Request) {
	if s.files == nil {
		http.Error(w, "file storage is not configured", http.StatusServiceUnavailable)
		return
	}
	name := baseName(r.PathValue("name"))
	f, err := s.files.Open(name)
	if err != nil {
		http.Error(w, "no such file", http.StatusNotFound)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	http.ServeContent(w, r, name, time.Time{}, f)
}

// fileDeliveryResult records what the tablet did with a file.
func (s *Server) fileDeliveryResult(w http.ResponseWriter, r *http.Request) {
	dev := deviceFrom(r.Context())
	if dev == nil {
		http.Error(w, "unknown device", http.StatusNotFound)
		return
	}
	name := baseName(r.PathValue("name"))
	var req struct {
		Status string `json:"status"`
		Error  string `json:"error"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	switch req.Status {
	case store.FileDeliveryDone, store.FileDeliveryFailed, store.FileDeliveryPending:
	default:
		http.Error(w, "bad status", http.StatusBadRequest)
		return
	}
	if err := s.st.SetFileDeliveryStatus(dev.ID, name, req.Status, strings.TrimSpace(req.Error)); err != nil {
		http.Error(w, "could not record the result", http.StatusInternalServerError)
		return
	}
	if req.Status == store.FileDeliveryFailed {
		// Worth surfacing: the operator believes the class has the worksheet.
		s.recordAs("device", "file_delivery_failed", store.EventWarn, dev.ID,
			fmt.Sprintf("%s could not save %s: %s",
				deviceLabel(dev.ID, dev.Name), name, strings.TrimSpace(req.Error)))
	}
	w.WriteHeader(http.StatusOK)
}

// ── Per-device inbox (the file manager) ──────────────────────────────────────
//
// The tablet is behind NAT, so the console cannot query it. Browsing a device's
// inbox is therefore a command that answers by uploading a listing, and what
// the console shows is the last answer plus when it arrived. A stale listing
// labelled with its age is more use than a spinner that never resolves because
// the tablet is in a cupboard.

// deviceInbox returns the cached listing for one device.
func (s *Server) deviceInbox(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.st.GetDevice(id); err != nil {
		writeErr(w, http.StatusNotFound, "unknown device")
		return
	}
	entriesJSON, fetchedAt := s.st.DeviceInbox(id)
	var entries []map[string]any
	if json.Unmarshal([]byte(entriesJSON), &entries) != nil {
		entries = []map[string]any{}
	}
	if entries == nil {
		entries = []map[string]any{}
	}
	writeJSON(w, map[string]any{
		"device_id": id,
		"entries":   entries,
		// Empty until the tablet has answered once, which the console shows as
		// "never" rather than pretending the folder is empty.
		"fetched_at": fetchedAt,
	})
}

// refreshDeviceInbox asks a tablet to report what is in its inbox.
func (s *Server) refreshDeviceInbox(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.st.GetDevice(id); err != nil {
		writeErr(w, http.StatusNotFound, "unknown device")
		return
	}
	if err := s.queueDeviceCommand(id, "list_inbox", nil); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not queue the request")
		return
	}
	writeJSON(w, map[string]any{"queued": true})
}

// deleteDeviceFile asks a tablet to remove one file from its inbox.
func (s *Server) deleteDeviceFile(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	name := r.PathValue("name")
	if _, err := s.st.GetDevice(id); err != nil {
		writeErr(w, http.StatusNotFound, "unknown device")
		return
	}
	if strings.TrimSpace(name) == "" {
		writeErr(w, http.StatusBadRequest, "file name is required")
		return
	}
	if err := s.queueDeviceCommand(id, "delete_inbox_file", map[string]any{"name": name}); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not queue the deletion")
		return
	}
	s.record(r, "device_file_deleted", store.EventWarn, id,
		"Asked "+id+" to delete "+name+" from its inbox")
	writeJSON(w, map[string]any{"queued": true})
}

// reportDeviceInbox is the tablet answering a list_inbox command.
func (s *Server) reportDeviceInbox(w http.ResponseWriter, r *http.Request) {
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
	// Bound what one device can store, so a tablet with a full folder cannot
	// push an unbounded blob into the database.
	if len(req.Entries) > 500 {
		req.Entries = req.Entries[:500]
	}
	blob, err := json.Marshal(req.Entries)
	if err != nil {
		http.Error(w, "bad entries", http.StatusBadRequest)
		return
	}
	if err := s.st.SetDeviceInbox(dev.ID, string(blob)); err != nil {
		http.Error(w, "could not store the listing", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// queueDeviceCommand enqueues a command and wakes the device.
func (s *Server) queueDeviceCommand(deviceID, cmdType string, params map[string]any) error {
	now := time.Now().UTC().Format(time.RFC3339)
	if params == nil {
		params = map[string]any{}
	}
	c := &store.Command{
		ID: "cmd-" + shortHash(deviceID+now+cmdType+fmt.Sprint(params)), DeviceID: deviceID,
		Type: cmdType, Params: store.MustJSON(params), Status: "pending", CreatedAt: now,
	}
	if err := s.st.EnqueueCommand(c); err != nil {
		return err
	}
	s.pokes.Enqueue(deviceID, device.Poke{Type: cmdType})
	return nil
}
