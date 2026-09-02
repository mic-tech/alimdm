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
	"encoding/json"
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

	// controlSessionGap is how long a quiet stretch has to be before the next
	// tap counts as a new session. Only the start of one is recorded — a line
	// in the feed per tap would bury everything else in it.
	controlSessionGap = 2 * time.Minute
)

// inputEvent is one thing an operator did to a tablet: a tap, a key, or text.
//
// Coordinates are fractions of the screen, not pixels. The console is clicking a
// JPEG that has been scaled down for the wire, and knows nothing about the
// display it came from; the device multiplies by its own size.
type inputEvent struct {
	Type string  `json:"type"`           // "tap" | "key" | "text"
	X    float64 `json:"x,omitempty"`    // 0..1 across
	Y    float64 `json:"y,omitempty"`    // 0..1 down
	Key  string  `json:"key,omitempty"`  // "back" | "home" | "enter" | an arrow
	Text string  `json:"text,omitempty"` // typed into whatever has focus
	Dir  string  `json:"dir,omitempty"`  // "up" | "down", for a scroll
}

// controlKeys is what an operator may press. A short list on purpose: these are
// the keys that move around a screen and commit what is on it, which is what
// remote control of a kiosk is for. Anything that needs a real keyboard goes as
// text, and anything that changes the device rather than the app on it is a
// command with a confirmation attached, not a keypress.
var controlKeys = map[string]bool{
	"back": true, "home": true, "enter": true,
	"up": true, "down": true, "left": true, "right": true,
}

// maxQueuedInput bounds what one device can have waiting. Input is only useful
// while it is fresh: a tap that arrives four seconds late lands on a screen
// that has moved on, so the oldest is dropped rather than the newest refused.
const maxQueuedInput = 16

// streamHub fans one tablet's frames out to the viewers watching it, and
// carries input back the other way.
type streamHub struct {
	mu       sync.Mutex
	subs     map[int64]chan []byte
	nextSub  int64
	wantedTo time.Time // while in the future, the tablet should keep capturing
	lastAt   time.Time
	frames   int64
	// input rides on the reply to the tablet's next frame POST. That request is
	// already made several times a second while someone is watching, and never
	// when nobody is — which is exactly when control is wanted and exactly when
	// it is not. No second connection, and latency of about one frame.
	input      []inputEvent
	lastInput  time.Time
	controlled bool // true from the first event of a session until it lapses
}

// queueInput adds one event for the tablet's next frame POST. Reports whether
// this is the start of a control session, so the caller can record it once
// rather than once per tap.
func (s *streamHub) queueInput(e inputEvent) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.input) >= maxQueuedInput {
		s.input = s.input[1:]
	}
	s.input = append(s.input, e)
	now := time.Now()
	starting := !s.controlled || now.Sub(s.lastInput) > controlSessionGap
	s.lastInput = now
	s.controlled = true
	return starting
}

// takeInput hands over everything queued and clears it.
func (s *streamHub) takeInput() []inputEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.input) == 0 {
		return nil
	}
	out := s.input
	s.input = nil
	return out
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

// sendInput hands one operator action to a tablet that is being watched.
//
// Only while a live view is running: input travels on the reply to a frame
// POST, so with nobody watching there is no channel and the event would sit in
// a queue until it was meaningless.
func (s *Server) sendInput(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	dev, err := s.st.GetDevice(id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "unknown device")
		return
	}
	var e inputEvent
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&e) != nil {
		writeErr(w, http.StatusBadRequest, "bad input")
		return
	}
	switch e.Type {
	case "tap":
		if e.X < 0 || e.X > 1 || e.Y < 0 || e.Y > 1 {
			writeErr(w, http.StatusBadRequest, "tap must be within the screen")
			return
		}
	case "key":
		if !controlKeys[e.Key] {
			writeErr(w, http.StatusBadRequest, "unknown key")
			return
		}
	case "scroll":
		// A scroll is a drag on a tablet, not a wheel: the device turns this
		// into a finger moving across the picture the operator is looking at.
		if e.Dir != "up" && e.Dir != "down" {
			writeErr(w, http.StatusBadRequest, "scroll must be up or down")
			return
		}
	case "text":
		if e.Text == "" {
			writeErr(w, http.StatusBadRequest, "no text")
			return
		}
		if len(e.Text) > 512 {
			e.Text = e.Text[:512]
		}
	default:
		writeErr(w, http.StatusBadRequest, "unknown input type")
		return
	}

	hub := s.streams.get(id)
	if !hub.wanted() {
		writeErr(w, http.StatusConflict, "start live view first: input reaches the device on the same channel as the frames")
		return
	}
	if hub.queueInput(e) {
		s.record(r, "control_started", store.EventWarn, dev.ID,
			"Started controlling "+deviceLabel(dev.ID, dev.Name))
	}
	writeJSON(w, map[string]any{"queued": true})
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
	resp := map[string]any{"continue": true, "viewers": hub.viewers()}
	// Anything an operator has done since the last frame goes back in the reply.
	if in := hub.takeInput(); len(in) > 0 {
		resp["input"] = in
	}
	writeJSON(w, resp)
}
