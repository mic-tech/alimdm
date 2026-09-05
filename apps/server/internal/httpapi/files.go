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
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"ali-mdm/server/internal/blob"
	"ali-mdm/server/internal/device"
	"ali-mdm/server/internal/store"
)

// fileKey reads a catalogue key out of a request path.
//
// A key is the file's path inside the library ("Juz30/CD1/Track 01.mp3"), so it
// arrives percent-encoded — Go's router keeps %2F inside one path segment and
// hands the decoded path back here. Basing it, as a flat name could be, would
// collapse every nested file onto its bare name and let a delete aimed at one
// folder land in another.
func fileKey(r *http.Request) string {
	return blob.SanitizeStoredPath(r.PathValue("name"))
}

// fileNameFor is the name a file should have inside its folder on the tablet.
// The catalogue key carries the folders to keep it unique, but a pupil should
// see "track01.mp3", not "Juz30 - Surah-078 - track01.mp3".
//
// Uploads record that name; for anything that predates the column it is the
// last segment of the key, which is the same thing.
func fileNameFor(f *store.File) string {
	if f.FileName != "" {
		return f.FileName
	}
	return path.Base(f.Name)
}

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

	// A folder upload sends the path the file had on the operator's machine, and
	// that path is what it gets: the key is the file's place in the library,
	// and the library on disk is laid out to match. Two "track01.mp3" in
	// different folders are two paths and stay two files, with nothing to fold
	// and nothing to disambiguate; uploading over one is an operator replacing
	// that file, which is what it should do.
	relPath := blob.SanitizeRelPath(r.FormValue("rel_path"))
	key := name
	if relPath != "" {
		key = relPath + "/" + name
	}

	sha, storedPath, size, err := s.files.Ingest(file, key, maxFileBytes)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	f := &store.File{
		Name: blob.SanitizeStoredPath(key), SHA256: sha, Size: size,
		ContentType: hdr.Header.Get("Content-Type"), Path: storedPath,
		RelPath: relPath, FileName: blob.Sanitize(name),
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
			// What the device saves it as, which is not the catalogue key.
			"file_name": fileNameFor(&f),
			// The folder the file belongs to. This hand-built map does not
			// marshal store.File, so a field added there does not appear here:
			// leaving rel_path out is what made a folder upload come back as a
			// flat list in the console however carefully it was uploaded.
			"rel_path": f.RelPath,
		})
	}
	writeJSON(w, out)
}

func (s *Server) deleteFile(w http.ResponseWriter, r *http.Request) {
	name := fileKey(r)
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
		"Deleted "+name+" from the library (copies already on devices are left alone)")
	writeJSON(w, map[string]any{"name": name, "deleted": true})
}

// pushRequest is the audience of a push: an explicit list of devices, one
// group, or — when neither is given — every enrolled device.
//
// Devices is a pointer so that a list sent and empty can be told apart from no
// list at all. They mean opposite things: an operator who picked devices and
// somehow sent none meant *no* devices, and treating that as "everyone" is the
// one mistake here that cannot be taken back.
type pushRequest struct {
	Devices *[]string `json:"devices"`
	GroupID string    `json:"group_id"`
}

// audience reads a push request, or says why it cannot, with the status the
// caller should answer with.
func (s *Server) audience(req pushRequest) ([]string, int, error) {
	if req.Devices != nil && len(*req.Devices) == 0 {
		return nil, http.StatusBadRequest, errors.New("no devices chosen")
	}
	var devices []string
	if req.Devices != nil {
		devices = *req.Devices
	}
	targets, err := s.pushTargets(devices, req.GroupID)
	if err != nil {
		return nil, http.StatusInternalServerError, errors.New("could not list devices")
	}
	if len(targets) == 0 {
		return nil, http.StatusBadRequest, errors.New("no devices to send to")
	}
	return targets, 0, nil
}

// pushTargets resolves an audience to device ids.
func (s *Server) pushTargets(devices []string, groupID string) ([]string, error) {
	if len(devices) > 0 {
		return devices, nil
	}
	devs, err := s.st.ListDevices()
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, d := range devs {
		// An empty group_id means the whole fleet.
		if groupID == "" || d.GroupID == groupID {
			out = append(out, d.ID)
		}
	}
	return out, nil
}

// queueFiles queues every file for every target that exists, and returns how
// many device/file pairs were queued.
//
// Each device is woken once at the end rather than once per file: sending a
// folder of thirty tracks should be one nudge, not thirty.
func (s *Server) queueFiles(targets, names []string) int {
	queued := 0
	for _, id := range targets {
		if _, err := s.st.GetDevice(id); err != nil {
			continue // skip unknown ids rather than failing the whole push
		}
		n := 0
		for _, name := range names {
			if s.st.QueueFileDelivery(id, name) == nil {
				n++
			}
		}
		if n > 0 {
			// Wake the device so the files land in seconds rather than at the
			// next heartbeat. Harmless when MQTT is not configured.
			s.pokes.Enqueue(id, device.Poke{Type: "fetch_files"})
		}
		queued += n
	}
	return queued
}

// filesUnder returns every catalogue entry inside a folder, at any depth.
//
// The folders are metadata, not directories — storage stays flat — so this is a
// scan and a prefix test rather than a walk. A school library is a few hundred
// rows, and doing it here keeps the store free of a query that only one caller
// would ever want.
func (s *Server) filesUnder(rel string) ([]store.File, error) {
	all, err := s.st.ListFiles()
	if err != nil {
		return nil, err
	}
	out := []store.File{}
	for _, f := range all {
		if f.RelPath == rel || strings.HasPrefix(f.RelPath, rel+"/") {
			out = append(out, f)
		}
	}
	return out, nil
}

// pushFile queues a file for a set of devices, a whole group, or the fleet.
func (s *Server) pushFile(w http.ResponseWriter, r *http.Request) {
	name := fileKey(r)
	if _, err := s.st.GetFile(name); err != nil {
		writeErr(w, http.StatusNotFound, "no such file")
		return
	}
	var req pushRequest
	_ = json.NewDecoder(r.Body).Decode(&req)

	targets, code, err := s.audience(req)
	if err != nil {
		writeErr(w, code, err.Error())
		return
	}

	queued := s.queueFiles(targets, []string{name})
	s.record(r, "file_pushed", store.EventInfo, "",
		fmt.Sprintf("Pushed %s to %s", name, plural(queued, "device", "devices")))
	writeJSON(w, map[string]any{"name": name, "queued": queued, "targets": len(targets)})
}

// pushFolder queues every file in a folder, at any depth, in one request.
//
// This exists because the alternative is real work: a folder of thirty tracks
// meant thirty trips through the send dialog, and the one an operator forgot
// was invisible afterwards.
//
// The folder travels in the body rather than the path because it contains
// slashes, and a ServeMux wildcard cannot hold those without swallowing the
// rest of the route.
func (s *Server) pushFolder(w http.ResponseWriter, r *http.Request) {
	var req struct {
		pushRequest
		RelPath string `json:"rel_path"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	rel := blob.SanitizeRelPath(req.RelPath)
	if rel == "" {
		writeErr(w, http.StatusBadRequest, "no folder given")
		return
	}
	files, err := s.filesUnder(rel)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not read the file library")
		return
	}
	if len(files) == 0 {
		writeErr(w, http.StatusNotFound, "no files in that folder")
		return
	}

	targets, code, err := s.audience(req.pushRequest)
	if err != nil {
		writeErr(w, code, err.Error())
		return
	}

	names := make([]string, 0, len(files))
	for _, f := range files {
		names = append(names, f.Name)
	}
	queued := s.queueFiles(targets, names)
	s.record(r, "file_pushed", store.EventInfo, "",
		fmt.Sprintf("Pushed the %s folder (%s) to %s", rel,
			plural(len(files), "file", "files"), plural(len(targets), "device", "devices")))
	writeJSON(w, map[string]any{
		"rel_path": rel, "files": len(files), "queued": queued, "targets": len(targets),
	})
}

// deleteFolder removes every file in a folder, at any depth, from the library.
//
// Copies already on tablets stay where they are, exactly as with a single file:
// the inbox is the pupil's folder, not a mirror of this one.
func (s *Server) deleteFolder(w http.ResponseWriter, r *http.Request) {
	rel := blob.SanitizeRelPath(r.URL.Query().Get("rel_path"))
	if rel == "" {
		writeErr(w, http.StatusBadRequest, "no folder given")
		return
	}
	files, err := s.filesUnder(rel)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not read the file library")
		return
	}
	if len(files) == 0 {
		writeErr(w, http.StatusNotFound, "no files in that folder")
		return
	}
	deleted := 0
	for _, f := range files {
		// Row first: a catalogue entry pointing at a missing file is worse than
		// an orphaned file, which simply stops being listed.
		if err := s.st.DeleteFile(f.Name); err != nil {
			continue
		}
		if s.files != nil {
			_ = s.files.Delete(f.Name)
		}
		deleted++
	}
	if deleted == 0 {
		writeErr(w, http.StatusInternalServerError, "could not delete the folder")
		return
	}
	s.record(r, "file_deleted", store.EventWarn, "",
		fmt.Sprintf("Deleted the %s folder (%s) from the library "+
			"(copies already on devices are left alone)", rel, plural(deleted, "file", "files")))
	writeJSON(w, map[string]any{"rel_path": rel, "deleted": deleted})
}

// fileDeliveries reports per-device delivery state for one file.
func (s *Server) fileDeliveries(w http.ResponseWriter, r *http.Request) {
	name := fileKey(r)
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
			// The folder to recreate under the inbox, and the name to use
			// inside it — the key is the whole path, so it is neither.
			"rel_path":  f.RelPath,
			"file_name": fileNameFor(f),
			// PathEscape, not raw: the key is a path, and both its separators
			// and any spaces in it have to survive as one segment. A literal
			// space in a request line is a 400 from net/http before a handler
			// ever sees it, and a literal "/" would split the route.
			"download_url": s.baseURL + "/api/v1/files/" + url.PathEscape(f.Name) + "/download",
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
	name := fileKey(r)
	f, err := s.files.Open(name)
	if err != nil {
		http.Error(w, "no such file", http.StatusNotFound)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	// The bare name: the key carries folders, and a Content-Disposition holding
	// a path is a header that anything downstream is entitled to distrust.
	w.Header().Set("Content-Disposition", `attachment; filename="`+path.Base(name)+`"`)
	http.ServeContent(w, r, name, time.Time{}, f)
}

// fileDeliveryResult records what the tablet did with a file.
func (s *Server) fileDeliveryResult(w http.ResponseWriter, r *http.Request) {
	dev := deviceFrom(r.Context())
	if dev == nil {
		http.Error(w, "unknown device", http.StatusNotFound)
		return
	}
	name := fileKey(r)
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
	// The folder comes from the query string rather than the path: a nested
	// entry is identified by (folder, name), and both in the path would need a
	// route that matches a variable number of segments.
	relPath := blob.SanitizeRelPath(r.URL.Query().Get("rel_path"))
	if err := s.queueDeviceCommand(id, "delete_inbox_file", map[string]any{
		"name": name, "rel_path": relPath,
	}); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not queue the deletion")
		return
	}
	label := id
	if dev, err := s.st.GetDevice(id); err == nil {
		label = deviceLabel(dev.ID, dev.Name)
	}
	where := name
	if relPath != "" {
		where = relPath + "/" + name
	}
	s.record(r, "device_file_deleted", store.EventWarn, id,
		"Asked "+label+" to delete "+where+" from its inbox")
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
