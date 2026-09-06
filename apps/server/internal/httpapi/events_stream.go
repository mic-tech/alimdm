package httpapi

// The wake stream: how a tablet hears about work in under a second.
//
// Nothing can connect *to* a tablet — it sits behind school NAT — so the device
// opens this and the server holds it, writing a line whenever something is
// queued. Server-sent events rather than a WebSocket because the traffic only
// goes one way: the device already has a perfectly good way to talk back, and a
// WebSocket would add a handshake, a framing layer and a proxy requirement to
// deliver the two words "wake up".
//
// It is a shortcut, never the mechanism. Every notice is empty and the device
// answers it by heartbeating exactly as it would have done anyway, so a stream
// that drops, stalls or never connects costs latency and nothing else. The
// thirty-second poll is still what makes the system work.

import (
	"fmt"
	"net/http"
	"time"
)

const (
	// streamKeepalive spaces out the comment lines that keep the connection
	// from being reaped. Shorter than any proxy idle timeout worth having, and
	// short enough that a device notices a dead connection promptly.
	streamKeepalive = 20 * time.Second

	// streamMaxAge is how long one connection is allowed to live before the
	// server closes it and the device reconnects.
	//
	// Deliberately not forever. A connection held for hours is a connection
	// pinned to whatever DNS said when it opened — which is exactly why tablets
	// kept talking to the old server after the address moved, and only followed
	// once they were rebooted. Recycling makes that self-healing.
	streamMaxAge = 5 * time.Minute
)

// deviceEvents holds a stream open and writes a line when the device has work.
func (s *Server) deviceEvents(w http.ResponseWriter, r *http.Request) {
	dev := deviceFrom(r.Context())
	if dev == nil {
		http.Error(w, "unknown device", http.StatusNotFound)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		// Without flushing this would buffer until the connection closed, which
		// is the opposite of the point.
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	// nginx buffers proxied responses by default, which would hold every notice
	// until a buffer filled — the classic "works locally, lags in production".
	// Saying so here means the proxy needs no special configuration.
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	notices, unsubscribe := s.pokes.Subscribe(dev.ID)
	defer unsubscribe()

	// Open with the recycle deadline, so the device schedules its own
	// reconnection rather than waiting to be cut off.
	fmt.Fprintf(w, "retry: 5000\nevent: ready\ndata: {\"max_age_seconds\":%d}\n\n",
		int(streamMaxAge.Seconds()))
	flusher.Flush()

	// Anything queued between the last heartbeat and this connection opening
	// would otherwise wait for the next one.
	if len(s.pokes.Peek(dev.ID)) > 0 {
		fmt.Fprint(w, "event: wake\ndata: {}\n\n")
		flusher.Flush()
	}

	keepalive := time.NewTicker(streamKeepalive)
	defer keepalive.Stop()
	deadline := time.NewTimer(streamMaxAge)
	defer deadline.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-deadline.C:
			// A clean goodbye: the device reconnects immediately instead of
			// discovering a dead socket on its next write.
			fmt.Fprint(w, "event: bye\ndata: {}\n\n")
			flusher.Flush()
			return
		case <-notices:
			fmt.Fprint(w, "event: wake\ndata: {}\n\n")
			flusher.Flush()
		case <-keepalive.C:
			// A comment line: valid SSE, ignored by the client, and enough to
			// keep every hop in between convinced the connection is alive.
			fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}
