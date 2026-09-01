package httpapi

// Live view of a tablet's screen.
//
// The tablet is behind school NAT, so nothing can connect *to* it. The frames
// therefore travel the only direction that works: the tablet posts them out to
// this server, which fans them out to whoever is watching in the console.
//
// Frames are posted one HTTP request each rather than as one long streaming
// upload. At the handful of frames per second this manages it costs little on a
// keep-alive connection, and it buys a lot: a dropped frame is just a dropped
// request, a flaky connection recovers by itself, and there is no half-parsed
// stream state to get wrong on either end.
//
// Nothing is stored. A frame lives in memory until the last viewer has had it,
// which keeps a picture of a classroom off the server's disk.

import (
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"ali-mdm/server/internal/device"
	"ali-mdm/server/internal/store"
)

const (
	// viewerLease is how long a viewer's presence keeps the stream alive. The
	// console renews it while the tab is open; if the browser is closed or the
	// laptop sleeps, the tablet stops capturing shortly after on its own.
	viewerLease = 20 * time.Second

	// armLease covers the wait for the tablet to notice. It learns from its
	// heartbeat, which is every 30s, so a lease shorter than that could lapse
	// before the tablet ever saw the request — the stream would then never
	// start, which is exactly what happened the first time this was tried.
	armLease = 90 * time.Second

	// frameQueue is per-viewer. Deliberately tiny: on a slow connection the
	// right thing is to skip to the newest frame, not to play a stale backlog.
	frameQueue = 2

	// maxFrameBytes bounds one frame.
	maxFrameBytes = 4 << 20
)

// streamHub fans one tablet's frames out to the viewers watching it.
type streamHub struct {
	mu       sync.Mutex
	subs     map[int64]chan []byte
	nextSub  int64
	wantedTo time.Time // while in the future, the tablet should keep capturing
	lastAt   time.Time
	frames   int64
}

type streamHubs struct {
	mu   sync.Mutex
	hubs map[string]*streamHub
}

func newStreamHubs() *streamHubs {
	return &streamHubs{hubs: map[string]*streamHub{}}
}

func (h *streamHubs) get(deviceID string) *streamHub {
	h.mu.Lock()
	defer h.mu.Unlock()
	hub := h.hubs[deviceID]
	if hub == nil {
		hub = &streamHub{subs: map[int64]chan []byte{}}
		h.hubs[deviceID] = hub
	}
	return hub
}

// request marks the stream as wanted for another lease. Never shortens an
// existing one: an arming window must survive a viewer's shorter renewals.
func (s *streamHub) request() { s.extend(viewerLease) }

// arm gives the tablet long enough to notice on its next heartbeat.
func (s *streamHub) arm() { s.extend(armLease) }

func (s *streamHub) extend(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if until := time.Now().Add(d); until.After(s.wantedTo) {
		s.wantedTo = until
	}
}

// wanted reports whether the tablet should be capturing right now.
func (s *streamHub) wanted() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return time.Now().Before(s.wantedTo)
}

func (s *streamHub) stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.wantedTo = time.Time{}
}

// subscribe returns a channel of frames and the function to release it.
func (s *streamHub) subscribe() (<-chan []byte, func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.nextSub
	s.nextSub++
	ch := make(chan []byte, frameQueue)
	s.subs[id] = ch
	return ch, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if c, ok := s.subs[id]; ok {
			delete(s.subs, id)
			close(c)
		}
	}
}

// publish hands a frame to every viewer, dropping the oldest for anyone who has
// fallen behind. One slow viewer must not stall the tablet's capture loop.
func (s *streamHub) publish(frame []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastAt = time.Now()
	s.frames++
	for _, ch := range s.subs {
		select {
		case ch <- frame:
		default:
			// Full: discard the oldest, then take the newest.
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- frame:
			default:
			}
		}
	}
}

func (s *streamHub) viewers() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.subs)
}

func (s *streamHub) status() (viewers int, frames int64, lastAt time.Time, live bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.subs), s.frames, s.lastAt, time.Now().Before(s.wantedTo)
}

// ── Operator-facing ──────────────────────────────────────────────────────────

// startStream asks a tablet to begin capturing.
//
// The tablet learns on its next heartbeat, so the first frame can be up to one
// heartbeat away. The console says so rather than showing a spinner that looks
// broken.
func (s *Server) startStream(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	dev, err := s.st.GetDevice(id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "unknown device")
		return
	}
	s.streams.get(id).arm()
	// Wake it if MQTT is configured; harmless when it is not.
	s.pokes.Enqueue(id, device.Poke{Type: "start_stream"})
	s.record(r, "stream_started", store.EventInfo, id,
		"Started live view of "+deviceLabel(dev.ID, dev.Name))
	writeJSON(w, map[string]any{"requested": true})
}

func (s *Server) stopStream(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.streams.get(id).stop()
	writeJSON(w, map[string]any{"stopped": true})
}

// streamStatus lets the console show what is happening before any frame lands.
func (s *Server) streamStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	viewers, frames, lastAt, live := s.streams.get(id).status()
	last := ""
	if !lastAt.IsZero() {
		last = lastAt.UTC().Format(time.RFC3339)
	}
	writeJSON(w, map[string]any{
		"requested":  live,
		"viewers":    viewers,
		"frames":     frames,
		"last_frame": last,
	})
}

// streamMJPEG is the viewer endpoint: an endless multipart response, one JPEG
// part per frame. Consumed by the console with fetch() so the API key travels
// in a header rather than a URL — an <img src> could not do that, and putting a
// credential in a query string would leak it into logs and history.
func (s *Server) streamMJPEG(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.st.GetDevice(id); err != nil {
		writeErr(w, http.StatusNotFound, "unknown device")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	hub := s.streams.get(id)
	// Arm rather than request: the viewer may attach before the tablet's first
	// heartbeat after the request, and a 20s lease would lapse in that gap.
	hub.arm()
	frames, release := hub.subscribe()
	defer release()

	const boundary = "alimdmframe"
	w.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary="+boundary)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "close")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	// Renew the tablet's lease while this viewer is connected, so capture stops
	// on its own shortly after the tab is closed.
	renew := time.NewTicker(viewerLease / 3)
	defer renew.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-renew.C:
			hub.request()
		case frame, ok := <-frames:
			if !ok {
				return
			}
			if _, err := fmt.Fprintf(w,
				"--%s\r\nContent-Type: image/jpeg\r\nContent-Length: %d\r\n\r\n",
				boundary, len(frame)); err != nil {
				return
			}
			if _, err := w.Write(frame); err != nil {
				return
			}
			if _, err := w.Write([]byte("\r\n")); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// ── Device-facing ────────────────────────────────────────────────────────────

// postFrame takes one JPEG from a tablet and tells it whether to keep going.
// The answer rides on the response so the capture loop needs no second channel
// to learn that the last viewer has gone.
func (s *Server) postFrame(w http.ResponseWriter, r *http.Request) {
	dev := deviceFrom(r.Context())
	if dev == nil {
		http.Error(w, "unknown device", http.StatusNotFound)
		return
	}
	hub := s.streams.get(dev.ID)
	if !hub.wanted() {
		// Nobody is watching: 409 is the tablet's signal to stop capturing.
		writeJSON(w, map[string]any{"continue": false})
		return
	}
	body := http.MaxBytesReader(w, r.Body, maxFrameBytes)
	frame, err := io.ReadAll(body)
	if err != nil {
		http.Error(w, "frame too large or unreadable", http.StatusBadRequest)
		return
	}
	if len(frame) == 0 {
		http.Error(w, "empty frame", http.StatusBadRequest)
		return
	}
	hub.publish(frame)
	writeJSON(w, map[string]any{"continue": true, "viewers": hub.viewers()})
}
