package httpapi

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"ali-mdm/server/internal/store"
)

// upload posts a file into the library the way the console does.
func (e *testEnv) upload(t *testing.T, tok, name string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(body); err != nil {
		t.Fatal(err)
	}
	mw.Close()

	req := httptest.NewRequest("POST", "/api/v1/files", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req)
	return rec
}

// The whole point of the feature: one upload, one push, every tablet gets it.
func TestPushReachesEveryDeviceInTheFleet(t *testing.T) {
	e := newTestEnv(t)
	tok := e.login("admin@x.com", "adminpassword")
	if err := e.st.UpsertGroup(&store.Group{
		ID: "default", Name: "Default", Config: "{}", ConfigHash: "h", ConfigVersion: 1,
	}); err != nil {
		t.Fatal(err)
	}
	keys := map[string]string{}
	for _, id := range []string{"tablet-1", "tablet-2", "tablet-3"} {
		keys[id] = e.newDeviceKey(t, id)
	}

	if rec := e.upload(t, tok, "worksheet.pdf", []byte("%PDF-1.4 fake")); rec.Code != http.StatusOK {
		t.Fatalf("upload: status %d (%s)", rec.Code, rec.Body.String())
	}
	rec, body := e.do("POST", "/api/v1/files/worksheet.pdf/push", tok, map[string]any{})
	if rec.Code != http.StatusOK {
		t.Fatalf("push: status %d (%s)", rec.Code, rec.Body.String())
	}
	if q, _ := body["queued"].(float64); q != 3 {
		t.Fatalf("queued = %v, want 3 — a push that misses tablets is the failure this feature exists to avoid", body["queued"])
	}

	// Each tablet must be offered the file exactly once.
	for id, key := range keys {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/api/v1/devices/"+id+"/files", nil)
		req.Header.Set("Authorization", "Bearer "+key)
		e.mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s pending files: status %d", id, rec.Code)
		}
		var got []map[string]any
		json.Unmarshal(rec.Body.Bytes(), &got)
		if len(got) != 1 || got[0]["name"] != "worksheet.pdf" {
			t.Fatalf("%s was offered %v, want one worksheet.pdf", id, got)
		}
	}
}

// A claimed file must not be handed out again, or a slow download would be
// restarted on every heartbeat.
func TestClaimedFilesAreNotOfferedTwice(t *testing.T) {
	e := newTestEnv(t)
	tok := e.login("admin@x.com", "adminpassword")
	key := e.newDeviceKey(t, "tablet-1")
	e.upload(t, tok, "notes.pdf", []byte("data"))
	e.do("POST", "/api/v1/files/notes.pdf/push", tok, map[string]any{})

	fetch := func() []map[string]any {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/api/v1/devices/tablet-1/files", nil)
		req.Header.Set("Authorization", "Bearer "+key)
		e.mux.ServeHTTP(rec, req)
		var got []map[string]any
		json.Unmarshal(rec.Body.Bytes(), &got)
		return got
	}
	if len(fetch()) != 1 {
		t.Fatal("precondition: the file should be offered once")
	}
	if again := fetch(); len(again) != 0 {
		t.Errorf("the file was offered again after being claimed: %v", again)
	}
}

// Delivery state is what tells an operator the class actually got the file.
func TestDeliveryStatusIsReportedPerDevice(t *testing.T) {
	e := newTestEnv(t)
	tok := e.login("admin@x.com", "adminpassword")
	okKey := e.newDeviceKey(t, "tablet-ok")
	badKey := e.newDeviceKey(t, "tablet-bad")
	e.upload(t, tok, "slides.pdf", []byte("data"))
	e.do("POST", "/api/v1/files/slides.pdf/push", tok, map[string]any{})

	report := func(id, key, status, errText string) {
		rec := httptest.NewRecorder()
		b, _ := json.Marshal(map[string]string{"status": status, "error": errText})
		req := httptest.NewRequest("POST", "/api/v1/devices/"+id+"/files/slides.pdf/result", bytes.NewReader(b))
		req.Header.Set("Authorization", "Bearer "+key)
		e.mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s result: status %d (%s)", id, rec.Code, rec.Body.String())
		}
	}
	report("tablet-ok", okKey, store.FileDeliveryDone, "")
	report("tablet-bad", badKey, store.FileDeliveryFailed, "no space left")

	rec, _ := e.do("GET", "/api/v1/files/slides.pdf/deliveries", tok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("deliveries: status %d", rec.Code)
	}
	var ds []map[string]any
	json.Unmarshal(rec.Body.Bytes(), &ds)
	states := map[string]string{}
	for _, d := range ds {
		states[d["device_id"].(string)] = d["status"].(string)
	}
	if states["tablet-ok"] != store.FileDeliveryDone {
		t.Errorf("tablet-ok = %q, want done", states["tablet-ok"])
	}
	if states["tablet-bad"] != store.FileDeliveryFailed {
		t.Errorf("tablet-bad = %q, want failed", states["tablet-bad"])
	}
}

// A device must not be able to fetch another device's inbox or report on it.
func TestOneDeviceCannotActForAnother(t *testing.T) {
	e := newTestEnv(t)
	tok := e.login("admin@x.com", "adminpassword")
	e.newDeviceKey(t, "tablet-1")
	otherKey := e.newDeviceKey(t, "tablet-2")
	e.upload(t, tok, "private.pdf", []byte("data"))
	e.do("POST", "/api/v1/files/private.pdf/push", tok, map[string]any{"devices": []string{"tablet-1"}})

	// tablet-2 asks for tablet-1's inbox using its own key.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/devices/tablet-1/files", nil)
	req.Header.Set("Authorization", "Bearer "+otherKey)
	e.mux.ServeHTTP(rec, req)
	var got []map[string]any
	json.Unmarshal(rec.Body.Bytes(), &got)
	if len(got) != 0 {
		t.Errorf("tablet-2 was handed tablet-1's files: %v", got)
	}

	// tablet-1's delivery must still be pending, not consumed by tablet-2.
	if n := e.st.CountPendingFileDeliveries("tablet-1"); n != 1 {
		t.Errorf("tablet-1 pending = %d, want 1 — another device consumed its delivery", n)
	}
}

// Deleting from the library must not strand a catalogue row pointing nowhere.
func TestDeleteRemovesFileAndDeliveries(t *testing.T) {
	e := newTestEnv(t)
	tok := e.login("admin@x.com", "adminpassword")
	e.newDeviceKey(t, "tablet-1")
	e.upload(t, tok, "old.pdf", []byte("data"))
	e.do("POST", "/api/v1/files/old.pdf/push", tok, map[string]any{})

	if rec, _ := e.do("DELETE", "/api/v1/files/old.pdf", tok, nil); rec.Code != http.StatusOK {
		t.Fatalf("delete: status %d", rec.Code)
	}
	if _, err := e.st.GetFile("old.pdf"); err == nil {
		t.Error("the catalogue row survived the delete")
	}
	if n := e.st.CountPendingFileDeliveries("tablet-1"); n != 0 {
		t.Errorf("pending deliveries survived the delete: %d", n)
	}
}

// The heartbeat has to tell the tablet there is something to fetch.
func TestHeartbeatAnnouncesPendingFiles(t *testing.T) {
	e := newTestEnv(t)
	tok := e.login("admin@x.com", "adminpassword")
	if err := e.st.UpsertGroup(&store.Group{
		ID: "default", Name: "Default", Config: "{}", ConfigHash: "h", ConfigVersion: 1,
	}); err != nil {
		t.Fatal(err)
	}
	key := e.newDeviceKey(t, "tablet-1")

	_, body := e.do("POST", "/api/v1/devices/tablet-1/heartbeat", key, map[string]any{})
	if n, _ := body["pending_files"].(float64); n != 0 {
		t.Fatalf("pending_files = %v before any push, want 0", body["pending_files"])
	}
	e.upload(t, tok, "homework.pdf", []byte("data"))
	e.do("POST", "/api/v1/files/homework.pdf/push", tok, map[string]any{})

	_, body = e.do("POST", "/api/v1/devices/tablet-1/heartbeat", key, map[string]any{})
	if n, _ := body["pending_files"].(float64); n != 1 {
		t.Errorf("pending_files = %v after a push, want 1 — the tablet would never fetch it", body["pending_files"])
	}
}

// ── Per-device file manager ──────────────────────────────────────────────────

// deviceCommands fetches what the tablet would collect on its next poll.
// The trailing slash matters: without it the request lands on the operator
// route instead of the device dispatcher, which is how these tests first failed.
func (e *testEnv) deviceCommands(t *testing.T, id, key string) []map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/devices/"+id+"/commands/", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	e.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("device commands: status %d (%s)", rec.Code, rec.Body.String())
	}
	var body struct {
		Commands []map[string]any `json:"commands"`
	}
	json.Unmarshal(rec.Body.Bytes(), &body)
	return body.Commands
}

// Asking a tablet what is in its inbox must reach it as a command, since the
// console cannot query a device behind NAT.
func TestRefreshQueuesAListingCommand(t *testing.T) {
	e := newTestEnv(t)
	tok := e.login("admin@x.com", "adminpassword")
	key := e.newDeviceKey(t, "tablet-1")

	if rec, _ := e.do("POST", "/api/v1/devices/tablet-1/inbox/refresh", tok, nil); rec.Code != http.StatusOK {
		t.Fatalf("refresh: status %d", rec.Code)
	}
	cmds := e.deviceCommands(t, "tablet-1", key)
	found := false
	for _, c := range cmds {
		if c["type"] == "list_inbox" {
			found = true
		}
	}
	if !found {
		t.Errorf("the tablet was never asked to list its inbox: %v", cmds)
	}
}

// The listing the tablet reports is what the console shows, with its age.
func TestReportedListingIsServedBackWithATimestamp(t *testing.T) {
	e := newTestEnv(t)
	tok := e.login("admin@x.com", "adminpassword")
	key := e.newDeviceKey(t, "tablet-1")

	// Before the tablet has ever answered, the console must be able to tell
	// "empty folder" from "never asked".
	_, body := e.do("GET", "/api/v1/devices/tablet-1/inbox", tok, nil)
	if body["fetched_at"] != "" {
		t.Errorf("fetched_at = %v before any report, want empty", body["fetched_at"])
	}

	rec := httptest.NewRecorder()
	payload, _ := json.Marshal(map[string]any{"entries": []map[string]any{
		{"name": "worksheet.pdf", "size": 1024, "mime_type": "application/pdf", "modified_at": 1.7e12},
	}})
	req := httptest.NewRequest("POST", "/api/v1/devices/tablet-1/inbox", bytes.NewReader(payload))
	req.Header.Set("Authorization", "Bearer "+key)
	e.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("report: status %d (%s)", rec.Code, rec.Body.String())
	}

	_, body = e.do("GET", "/api/v1/devices/tablet-1/inbox", tok, nil)
	entries, _ := body["entries"].([]any)
	if len(entries) != 1 {
		t.Fatalf("entries = %v, want the one file the tablet reported", body["entries"])
	}
	first, _ := entries[0].(map[string]any)
	if first["name"] != "worksheet.pdf" {
		t.Errorf("name = %v, want worksheet.pdf", first["name"])
	}
	if body["fetched_at"] == "" {
		t.Error("fetched_at is empty after a report; the console could not show the listing's age")
	}
}

// A device reports only its own inbox: the id comes from its key, not the path.
func TestDeviceCannotReportAnotherDevicesInbox(t *testing.T) {
	e := newTestEnv(t)
	tok := e.login("admin@x.com", "adminpassword")
	e.newDeviceKey(t, "tablet-1")
	otherKey := e.newDeviceKey(t, "tablet-2")

	rec := httptest.NewRecorder()
	payload, _ := json.Marshal(map[string]any{"entries": []map[string]any{{"name": "planted.pdf"}}})
	// tablet-2's key, but tablet-1 in the path.
	req := httptest.NewRequest("POST", "/api/v1/devices/tablet-1/inbox", bytes.NewReader(payload))
	req.Header.Set("Authorization", "Bearer "+otherKey)
	e.mux.ServeHTTP(rec, req)

	_, body := e.do("GET", "/api/v1/devices/tablet-1/inbox", tok, nil)
	if entries, _ := body["entries"].([]any); len(entries) != 0 {
		t.Errorf("tablet-2 wrote into tablet-1's listing: %v", body["entries"])
	}
}

// Deleting a file on a tablet is a command carrying the file name.
func TestDeleteQueuesACommandNamingTheFile(t *testing.T) {
	e := newTestEnv(t)
	tok := e.login("admin@x.com", "adminpassword")
	key := e.newDeviceKey(t, "tablet-1")

	if rec, _ := e.do("DELETE", "/api/v1/devices/tablet-1/inbox/worksheet.pdf", tok, nil); rec.Code != http.StatusOK {
		t.Fatalf("delete: status %d", rec.Code)
	}
	cmds := e.deviceCommands(t, "tablet-1", key)
	for _, c := range cmds {
		if c["type"] == "delete_inbox_file" {
			params, _ := c["params"].(map[string]any)
			if params["name"] != "worksheet.pdf" {
				t.Errorf("command names %v, want worksheet.pdf", params["name"])
			}
			return
		}
	}
	t.Errorf("no delete_inbox_file command reached the tablet: %v", cmds)
}

// uploadInto posts a file the way the console does for a folder upload: the
// same multipart form, plus the folder the file sat in on the operator's
// machine.
func (e *testEnv) uploadInto(t *testing.T, tok, relPath, name string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := mw.WriteField("rel_path", relPath); err != nil {
		t.Fatal(err)
	}
	mw.Close()

	req := httptest.NewRequest("POST", "/api/v1/files", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req)
	return rec
}

// seedLibrary fills the catalogue with a small nested tree and returns the
// login token, so the folder tests can say what they are actually about.
func (e *testEnv) seedLibrary(t *testing.T) string {
	t.Helper()
	tok := e.login("admin@x.com", "adminpassword")
	for _, f := range []struct{ rel, name string }{
		{"Juz30/Surah-078", "track01.mp3"},
		{"Juz30/Surah-078", "track02.mp3"},
		{"Juz30/Surah-079", "track01.mp3"},
		{"Handouts", "worksheet.pdf"},
		{"", "notice.pdf"},
	} {
		if rec := e.uploadInto(t, tok, f.rel, f.name, []byte("body of "+f.rel+"/"+f.name)); rec.Code != http.StatusOK {
			t.Fatalf("upload %s/%s: status %d (%s)", f.rel, f.name, rec.Code, rec.Body.String())
		}
	}
	return tok
}

// The reason folder push exists: a folder of tracks was thirty trips through
// the send dialog, and the one that got missed was invisible afterwards.
func TestFolderPushSendsEveryFileBeneathIt(t *testing.T) {
	e := newTestEnv(t)
	tok := e.seedLibrary(t)
	for _, id := range []string{"tablet-1", "tablet-2"} {
		e.newDeviceKey(t, id)
	}

	rec, body := e.do("POST", "/api/v1/folders/push", tok, map[string]any{"rel_path": "Juz30"})
	if rec.Code != http.StatusOK {
		t.Fatalf("push: status %d (%s)", rec.Code, rec.Body.String())
	}
	// Three files under Juz30, across two sub-folders — and not the handout or
	// the notice sitting outside it.
	if n, _ := body["files"].(float64); n != 3 {
		t.Fatalf("files = %v, want 3", body["files"])
	}
	if q, _ := body["queued"].(float64); q != 6 {
		t.Fatalf("queued = %v, want 6 (3 files × 2 tablets)", body["queued"])
	}

	ds, err := e.st.ClaimPendingFileDeliveries("tablet-1")
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range ds {
		if d.FileName == "notice.pdf" || d.FileName == "Handouts - worksheet.pdf" {
			t.Fatalf("%s was queued, but it is not in the Juz30 folder", d.FileName)
		}
	}
	if len(ds) != 3 {
		t.Fatalf("tablet-1 has %d pending files, want 3", len(ds))
	}
}

// A sub-folder is a folder too: sending one surah should not send the juz.
func TestFolderPushCanTargetASubFolder(t *testing.T) {
	e := newTestEnv(t)
	tok := e.seedLibrary(t)
	e.newDeviceKey(t, "tablet-1")

	rec, body := e.do("POST", "/api/v1/folders/push", tok, map[string]any{"rel_path": "Juz30/Surah-078"})
	if rec.Code != http.StatusOK {
		t.Fatalf("push: status %d (%s)", rec.Code, rec.Body.String())
	}
	if n, _ := body["files"].(float64); n != 2 {
		t.Fatalf("files = %v, want 2", body["files"])
	}
}

// A folder name that never existed should say so, rather than quietly reporting
// a successful push of nothing.
func TestFolderPushRejectsAnUnknownFolder(t *testing.T) {
	e := newTestEnv(t)
	tok := e.seedLibrary(t)
	e.newDeviceKey(t, "tablet-1")

	if rec, _ := e.do("POST", "/api/v1/folders/push", tok, map[string]any{"rel_path": "Juz29"}); rec.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", rec.Code)
	}
	// "Juz3" is a prefix of "Juz30" as a string, but it is not a parent folder.
	if rec, _ := e.do("POST", "/api/v1/folders/push", tok, map[string]any{"rel_path": "Juz3"}); rec.Code != http.StatusNotFound {
		t.Fatalf("prefix match: status %d, want 404 — Juz3 is not a parent of Juz30", rec.Code)
	}
}

func TestFolderDeleteLeavesTheRestOfTheLibraryAlone(t *testing.T) {
	e := newTestEnv(t)
	tok := e.seedLibrary(t)

	rec, body := e.do("DELETE", "/api/v1/folders?rel_path=Juz30%2FSurah-078", tok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete: status %d (%s)", rec.Code, rec.Body.String())
	}
	if n, _ := body["deleted"].(float64); n != 2 {
		t.Fatalf("deleted = %v, want 2", body["deleted"])
	}
	files, err := e.st.ListFiles()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 {
		t.Fatalf("%d files left, want 3", len(files))
	}
	for _, f := range files {
		if f.RelPath == "Juz30/Surah-078" {
			t.Fatalf("%s survived the folder delete", f.Name)
		}
	}
}

// A rel_path is operator input like any other: it must not be able to reach
// files outside the library, whatever it claims to be.
func TestFolderPushCannotEscapeTheLibrary(t *testing.T) {
	e := newTestEnv(t)
	tok := e.seedLibrary(t)
	e.newDeviceKey(t, "tablet-1")

	for _, rel := range []string{"../..", "/", "..", "Juz30/../Handouts/.."} {
		rec, _ := e.do("POST", "/api/v1/folders/push", tok, map[string]any{"rel_path": rel})
		if rec.Code == http.StatusOK {
			t.Fatalf("rel_path %q was accepted", rel)
		}
	}
}

// The console draws its folder tree from this field, and the list handler builds
// its rows by hand rather than marshalling store.File — so a folder upload came
// back looking flat. Guard the field, not the struct.
func TestListReportsTheFolderEachFileIsIn(t *testing.T) {
	e := newTestEnv(t)
	tok := e.seedLibrary(t)

	rec, _ := e.do("GET", "/api/v1/files", tok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: status %d (%s)", rec.Code, rec.Body.String())
	}
	var rows []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"Juz30 - Surah-078 - track01.mp3": "Juz30/Surah-078",
		"Juz30 - Surah-078 - track02.mp3": "Juz30/Surah-078",
		"Juz30 - Surah-079 - track01.mp3": "Juz30/Surah-079",
		"Handouts - worksheet.pdf":        "Handouts",
		"notice.pdf":                      "",
	}
	if len(rows) != len(want) {
		t.Fatalf("%d rows, want %d", len(rows), len(want))
	}
	for _, row := range rows {
		name, _ := row["name"].(string)
		rel, ok := row["rel_path"].(string)
		if !ok {
			t.Fatalf("%s has no rel_path — the console would draw it at the top level", name)
		}
		if w, known := want[name]; !known || rel != w {
			t.Fatalf("%s: rel_path = %q, want %q", name, rel, w)
		}
	}
}

// The library is stored flat, so the folder path is folded into the file's
// catalogue key. " - " is not reserved, though, which means two files in
// different folders can fold to the same key — and the second upload used to
// replace the first with no error at all.
func TestTwoFilesThatFoldToTheSameNameBothSurvive(t *testing.T) {
	e := newTestEnv(t)
	tok := e.login("admin@x.com", "adminpassword")

	// "Track 01.mp3" inside Juz30/CD1 …
	if rec := e.uploadInto(t, tok, "Juz30/CD1", "Track 01.mp3", []byte("inside CD1")); rec.Code != http.StatusOK {
		t.Fatalf("first upload: status %d (%s)", rec.Code, rec.Body.String())
	}
	// … and "CD1 - Track 01.mp3" sitting at the top of Juz30. Both fold to
	// "Juz30 - CD1 - Track 01.mp3".
	if rec := e.uploadInto(t, tok, "Juz30", "CD1 - Track 01.mp3", []byte("top of Juz30")); rec.Code != http.StatusOK {
		t.Fatalf("second upload: status %d (%s)", rec.Code, rec.Body.String())
	}

	files, err := e.st.ListFiles()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("%d files in the library, want 2 — one upload replaced the other", len(files))
	}
	byFolder := map[string]store.File{}
	for _, f := range files {
		byFolder[f.RelPath] = f
	}
	if got := byFolder["Juz30/CD1"].Size; got != int64(len("inside CD1")) {
		t.Fatalf("the file in Juz30/CD1 is %d bytes, want %d", got, len("inside CD1"))
	}
	if got := byFolder["Juz30"].Size; got != int64(len("top of Juz30")) {
		t.Fatalf("the file at the top of Juz30 is %d bytes, want %d", got, len("top of Juz30"))
	}
	// Each still reports the name a pupil should see.
	for rel, f := range byFolder {
		want := "Track 01.mp3"
		if rel == "Juz30" {
			want = "CD1 - Track 01.mp3"
		}
		if got := fileNameFor(&f); got != want {
			t.Fatalf("%s: device would save it as %q, want %q", rel, got, want)
		}
	}
}

// Uploading the same file into the same folder is an operator replacing it, and
// must not pile up "(2)", "(3)" copies.
func TestReuploadingIntoTheSameFolderReplaces(t *testing.T) {
	e := newTestEnv(t)
	tok := e.login("admin@x.com", "adminpassword")

	e.uploadInto(t, tok, "Juz30/CD1", "Track 01.mp3", []byte("first"))
	e.uploadInto(t, tok, "Juz30/CD1", "Track 01.mp3", []byte("second, corrected"))

	files, err := e.st.ListFiles()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("%d files, want 1 — a re-upload should replace, not duplicate", len(files))
	}
	if files[0].Size != int64(len("second, corrected")) {
		t.Fatalf("the library kept the old bytes")
	}
}
