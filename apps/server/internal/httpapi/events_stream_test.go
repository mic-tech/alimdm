package httpapi

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"ali-mdm/server/internal/device"
)

// readStream opens the wake stream against a real server and returns the lines
// it receives, until the caller cancels it.
func readStream(t *testing.T, e *testEnv, devKey string) (lines chan string, stop func()) {
	t.Helper()
	srv := httptest.NewServer(e.mux)
	req, err := http.NewRequest("GET", srv.URL+"/api/v1/devices/tablet-1/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+devKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stream: status %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("Content-Type %q, want text/event-stream", ct)
	}
	// Without this a proxy would hold every notice until a buffer filled, which
	// is the failure that only shows up in production.
	if resp.Header.Get("X-Accel-Buffering") != "no" {
		t.Fatal("X-Accel-Buffering is not set — a buffering proxy would break this")
	}

	out := make(chan string, 64)
	var once sync.Once
	done := make(chan struct{})
	go func() {
		defer close(out)
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			select {
			case out <- sc.Text():
			case <-done:
				return
			}
		}
	}()
	return out, func() {
		once.Do(func() {
			close(done)
			resp.Body.Close()
			srv.Close()
		})
	}
}

// waitFor reads until a line matching want appears, or gives up.
func waitFor(t *testing.T, lines chan string, want string, within time.Duration) {
	t.Helper()
	deadline := time.After(within)
	for {
		select {
		case l, ok := <-lines:
			if !ok {
				t.Fatalf("stream closed before %q arrived", want)
			}
			if strings.Contains(l, want) {
				return
			}
		case <-deadline:
			t.Fatalf("%q did not arrive within %s", want, within)
		}
	}
}

// The point of the feature: queue work and the device hears about it now,
// rather than up to thirty seconds later.
func TestQueuedWorkWakesAnOpenStream(t *testing.T) {
	e := newTestEnv(t)
	devKey := e.newDeviceKey(t, "tablet-1")

	lines, stop := readStream(t, e, devKey)
	defer stop()
	waitFor(t, lines, "event: ready", 2*time.Second)

	// Anything that queues work goes through Enqueue, so this is what a command,
	// a file push and an install all do.
	e.srv.pokes.Enqueue("tablet-1", device.Poke{Type: "reboot"})
	waitFor(t, lines, "event: wake", 2*time.Second)
}

// The stream says "there is work" and nothing else. If it consumed the notice,
// the heartbeat that follows would find nothing waiting and do nothing.
func TestTheStreamDoesNotConsumeTheWork(t *testing.T) {
	e := newTestEnv(t)
	devKey := e.newDeviceKey(t, "tablet-1")

	lines, stop := readStream(t, e, devKey)
	defer stop()
	waitFor(t, lines, "event: ready", 2*time.Second)

	e.srv.pokes.Enqueue("tablet-1", device.Poke{Type: "fetch_files"})
	waitFor(t, lines, "event: wake", 2*time.Second)

	// The heartbeat still finds it.
	if got := e.srv.pokes.Drain("tablet-1"); len(got) != 1 || got[0].Type != "fetch_files" {
		t.Fatalf("drained %+v — the stream ate the work it was only meant to announce", got)
	}
}

// Work queued while nothing was listening must not be missed by a stream that
// opens a moment later.
func TestAStreamOpeningLateStillHearsAboutWaitingWork(t *testing.T) {
	e := newTestEnv(t)
	devKey := e.newDeviceKey(t, "tablet-1")

	e.srv.pokes.Enqueue("tablet-1", device.Poke{Type: "screenshot"})

	lines, stop := readStream(t, e, devKey)
	defer stop()
	waitFor(t, lines, "event: wake", 2*time.Second)
}

// One device's work must never wake another's stream.
func TestAStreamOnlyHearsItsOwnDevice(t *testing.T) {
	e := newTestEnv(t)
	devKey := e.newDeviceKey(t, "tablet-1")
	e.newDeviceKey(t, "tablet-2")

	lines, stop := readStream(t, e, devKey)
	defer stop()
	waitFor(t, lines, "event: ready", 2*time.Second)

	e.srv.pokes.Enqueue("tablet-2", device.Poke{Type: "reboot"})

	// Nothing should arrive for tablet-1. A keepalive may, and is not a wake.
	deadline := time.After(600 * time.Millisecond)
	for {
		select {
		case l, ok := <-lines:
			if !ok {
				return
			}
			if strings.Contains(l, "event: wake") {
				t.Fatal("tablet-1's stream woke for tablet-2's work")
			}
		case <-deadline:
			return
		}
	}
}

// Subscribers must be released when a stream ends, or every reconnection leaks
// a channel that is written to for ever.
func TestSubscribersAreReleasedWhenAStreamEnds(t *testing.T) {
	e := newTestEnv(t)
	devKey := e.newDeviceKey(t, "tablet-1")

	lines, stop := readStream(t, e, devKey)
	waitFor(t, lines, "event: ready", 2*time.Second)
	if n := e.srv.pokes.Waiting("tablet-1"); n != 1 {
		t.Fatalf("%d subscribers while one stream is open, want 1", n)
	}
	stop()

	deadline := time.After(3 * time.Second)
	for e.srv.pokes.Waiting("tablet-1") != 0 {
		select {
		case <-deadline:
			t.Fatalf("%d subscribers left after the stream closed", e.srv.pokes.Waiting("tablet-1"))
		default:
			time.Sleep(20 * time.Millisecond)
		}
	}
}

// Enqueue is on the operator's request path. A device that has stopped reading
// must not be able to hold up the console.
func TestASlowReaderCannotBlockTheOperator(t *testing.T) {
	q := NewPokeQueue()
	_, unsubscribe := q.Subscribe("tablet-1")
	defer unsubscribe()

	done := make(chan struct{})
	go func() {
		// Far more than the channel's buffer of one, with nobody reading.
		for i := 0; i < 1000; i++ {
			q.Enqueue("tablet-1", device.Poke{Type: "screenshot"})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Enqueue blocked on a subscriber that was not reading")
	}
}

// An unauthenticated caller gets nothing.
func TestTheStreamRequiresADeviceKey(t *testing.T) {
	e := newTestEnv(t)
	e.newDeviceKey(t, "tablet-1")

	req := httptest.NewRequest("GET", "/api/v1/devices/tablet-1/events", nil)
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d without a key, want 401", rec.Code)
	}
}
