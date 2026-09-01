package httpapi

// Device snapshots.
//
// A still of each tablet's screen, refreshed on demand, so the console's card
// view can show what a room is actually looking at without holding a live
// stream open per tablet.
//
// The tablet has always uploaded these — the screenshot command captures and
// posts one — but the server threw the bytes away, so nothing could ever be
// shown. They are now kept in memory only: a picture of a classroom has no
// business on disk, and one frame per device is small enough that the cap here
// is about bounding a leak rather than saving space.

import (
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"
)

const (
	// maxSnapshotBytes bounds one upload. Screens compress small; anything far
	// larger is not a screenshot.
	maxSnapshotBytes = 8 << 20

	// snapshotTTL is how long a still is worth showing. Past this the console
	// says how old it is rather than pretending it is current.
	snapshotTTL = 10 * time.Minute
)

type snapshot struct {
	data        []byte
	contentType string
	at          time.Time
}

// snapshotStore holds the latest still per device.
type snapshotStore struct {
	mu   sync.RWMutex
	byID map[string]snapshot
}

func newSnapshotStore() *snapshotStore {
	return &snapshotStore{byID: map[string]snapshot{}}
}

func (s *snapshotStore) put(deviceID string, data []byte, contentType string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byID[deviceID] = snapshot{data: data, contentType: contentType, at: time.Now()}
}

func (s *snapshotStore) get(deviceID string) (snapshot, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	snap, ok := s.byID[deviceID]
	return snap, ok
}

func (s *snapshotStore) drop(deviceID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.byID, deviceID)
}

// ── Device-facing ────────────────────────────────────────────────────────────

// screenshot receives a still from a tablet and keeps the latest one.
func (s *Server) screenshot(w http.ResponseWriter, r *http.Request) {
	dev := deviceFrom(r.Context())
	if dev == nil {
		http.Error(w, "unknown device", http.StatusNotFound)
		return
	}
	if err := r.ParseMultipartForm(maxSnapshotBytes); err != nil {
		http.Error(w, "bad upload", http.StatusBadRequest)
		return
	}
	file, hdr, err := r.FormFile("image")
	if err != nil {
		http.Error(w, "missing image", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// One byte past the cap is enough to know it was exceeded.
	data, err := io.ReadAll(io.LimitReader(file, maxSnapshotBytes+1))
	if err != nil || len(data) > maxSnapshotBytes {
		http.Error(w, "image too large or unreadable", http.StatusBadRequest)
		return
	}
	if len(data) == 0 {
		http.Error(w, "empty image", http.StatusBadRequest)
		return
	}
	ct := hdr.Header.Get("Content-Type")
	if ct == "" {
		ct = "image/png"
	}
	s.snapshots.put(dev.ID, data, ct)
	w.WriteHeader(http.StatusOK)
}

// ── Operator-facing ──────────────────────────────────────────────────────────

// deviceSnapshot serves the latest still for one device.
func (s *Server) deviceSnapshot(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	snap, ok := s.snapshots.get(id)
	if !ok {
		// Not an error: the tablet simply has not sent one yet, and the console
		// shows a placeholder rather than a broken image.
		http.Error(w, "no snapshot yet", http.StatusNotFound)
		return
	}
	age := time.Since(snap.at)
	w.Header().Set("Content-Type", snap.contentType)
	w.Header().Set("Cache-Control", "no-store")
	// Lets the console label a still as stale without a second request.
	w.Header().Set("X-Snapshot-Age-Seconds", strconv.Itoa(int(age.Seconds())))
	w.Header().Set("X-Snapshot-At", snap.at.UTC().Format(time.RFC3339))
	w.Write(snap.data)
}

// requestSnapshot asks a tablet for a fresh still. The tablet acts on it at its
// next check-in, so the console keeps showing the previous one until it lands
// rather than blanking the card.
func (s *Server) requestSnapshot(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	dev, err := s.st.GetDevice(id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "unknown device")
		return
	}
	if err := s.queueDeviceCommand(dev.ID, "screenshot", nil); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not ask for a snapshot")
		return
	}
	// Deliberately not recorded in the notification feed: the card view asks
	// every tablet for one every 30 seconds, which would bury every real event.
	writeJSON(w, map[string]any{"requested": true})
}

// snapshotMeta reports what the console needs to decide whether to show a still
// and how to label its age, without downloading the image itself.
func (s *Server) snapshotMeta(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	snap, ok := s.snapshots.get(id)
	if !ok {
		writeJSON(w, map[string]any{"has_snapshot": false})
		return
	}
	writeJSON(w, map[string]any{
		"has_snapshot": true,
		"at":           snap.at.UTC().Format(time.RFC3339),
		"age_seconds":  int(time.Since(snap.at).Seconds()),
		"stale":        time.Since(snap.at) > snapshotTTL,
	})
}
