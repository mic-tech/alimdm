package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// postFrame sends one frame as the tablet would.
func (e *testEnv) postFrame(t *testing.T, id, key string, frame []byte) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest("POST", "/api/v1/devices/"+id+"/stream/frame", bytes.NewReader(frame))
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "image/jpeg")
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req)
	out := map[string]any{}
	json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

// A tablet must not capture when nobody is watching: that is battery and
// bandwidth spent on a picture no one sees.
func TestTabletIsToldToStopWhenNobodyIsWatching(t *testing.T) {
	e := newTestEnv(t)
	key := e.newDeviceKey(t, "tablet-1")

	code, body := e.postFrame(t, "tablet-1", key, []byte("\xff\xd8jpeg"))
	if code != http.StatusOK {
		t.Fatalf("frame post: status %d", code)
	}
	if body["continue"] != false {
		t.Errorf(`continue = %v with no viewers, want false — the tablet would keep capturing forever`, body["continue"])
	}
}

// Asking for a stream must reach the tablet through the heartbeat, which is the
// only channel that exists when MQTT is not configured.
func TestHeartbeatCarriesTheStreamRequest(t *testing.T) {
	e := newTestEnv(t)
	tok := e.login("admin@x.com", "adminpassword")
	if err := e.st.UpsertGroup(&testGroup); err != nil {
		t.Fatal(err)
	}
	key := e.newDeviceKey(t, "tablet-1")

	_, body := e.do("POST", "/api/v1/devices/tablet-1/heartbeat", key, map[string]any{})
	if body["stream_requested"] != false {
		t.Fatalf("stream_requested = %v before anyone asked, want false", body["stream_requested"])
	}

	if rec, _ := e.do("POST", "/api/v1/devices/tablet-1/stream/start", tok, nil); rec.Code != http.StatusOK {
		t.Fatalf("start: status %d", rec.Code)
	}
	_, body = e.do("POST", "/api/v1/devices/tablet-1/heartbeat", key, map[string]any{})
	if body["stream_requested"] != true {
		t.Errorf("stream_requested = %v after a start, want true — the tablet would never begin", body["stream_requested"])
	}

	// And stopping must reach it too, or the tablet captures until the lease runs out.
	e.do("POST", "/api/v1/devices/tablet-1/stream/stop", tok, nil)
	_, body = e.do("POST", "/api/v1/devices/tablet-1/heartbeat", key, map[string]any{})
	if body["stream_requested"] != false {
		t.Errorf("stream_requested = %v after a stop, want false", body["stream_requested"])
	}
}

// Frames posted by a tablet must reach a connected viewer intact.
func TestFramesReachTheViewer(t *testing.T) {
	e := newTestEnv(t)
	tok := e.login("admin@x.com", "adminpassword")
	key := e.newDeviceKey(t, "tablet-1")

	hub := e.srv.streams.get("tablet-1")
	frames, release := hub.subscribe()
	defer release()
	hub.request()

	want := []byte("\xff\xd8\xff\xe0 frame one")
	code, body := e.postFrame(t, "tablet-1", key, want)
	if code != http.StatusOK {
		t.Fatalf("frame post: status %d", code)
	}
	if body["continue"] != true {
		t.Errorf("continue = %v with a viewer attached, want true", body["continue"])
	}
	select {
	case got := <-frames:
		if !bytes.Equal(got, want) {
			t.Errorf("viewer got %q, want %q", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the frame never reached the viewer")
	}
	_ = tok
}

// A viewer that stops reading must not stall the tablet. The hub drops the
// oldest frame rather than blocking the capture loop behind a slow browser.
func TestASlowViewerNeverBlocksTheTablet(t *testing.T) {
	e := newTestEnv(t)
	key := e.newDeviceKey(t, "tablet-1")
	hub := e.srv.streams.get("tablet-1")
	_, release := hub.subscribe() // subscribed, but never reads
	defer release()
	hub.request()

	done := make(chan struct{})
	go func() {
		defer close(done)
		// Far more frames than the queue holds.
		for i := 0; i < 50; i++ {
			e.postFrame(t, "tablet-1", key, []byte{0xff, 0xd8, byte(i)})
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("posting frames blocked on a viewer that was not reading")
	}
}

// The newest frame is the one worth having: a viewer that falls behind should
// catch up to the present, not replay a backlog.
func TestSlowViewerGetsTheNewestFrames(t *testing.T) {
	hub := &streamHub{subs: map[int64]chan []byte{}}
	hub.request()
	frames, release := hub.subscribe()
	defer release()

	for i := 0; i < 10; i++ {
		hub.publish([]byte{byte(i)})
	}
	// Whatever is queued must come from the end of the run, not the start.
	got := <-frames
	if got[0] < 10-byte(frameQueue)-1 {
		t.Errorf("queued frame %d is stale; the viewer is replaying a backlog", got[0])
	}
}

// An empty or oversized frame must be refused rather than fanned out.
func TestBadFramesAreRejected(t *testing.T) {
	e := newTestEnv(t)
	key := e.newDeviceKey(t, "tablet-1")
	e.srv.streams.get("tablet-1").request()

	if code, _ := e.postFrame(t, "tablet-1", key, []byte{}); code != http.StatusBadRequest {
		t.Errorf("empty frame: status %d, want 400", code)
	}
	if code, _ := e.postFrame(t, "tablet-1", key, bytes.Repeat([]byte{0}, maxFrameBytes+1024)); code != http.StatusBadRequest {
		t.Errorf("oversized frame: status %d, want 400", code)
	}
}

// One tablet's frames must never appear in another's stream.
func TestFramesAreScopedToTheirDevice(t *testing.T) {
	e := newTestEnv(t)
	e.newDeviceKey(t, "tablet-1")
	otherKey := e.newDeviceKey(t, "tablet-2")

	hub1 := e.srv.streams.get("tablet-1")
	frames, release := hub1.subscribe()
	defer release()
	hub1.request()
	e.srv.streams.get("tablet-2").request()

	// tablet-2 posts, naming tablet-1 in the path.
	e.postFrame(t, "tablet-1", otherKey, []byte("\xff\xd8from tablet-2"))

	select {
	case got := <-frames:
		t.Errorf("tablet-2's frame appeared in tablet-1's stream: %q", got)
	case <-time.After(300 * time.Millisecond):
		// Correct: the id comes from the API key, not the path.
	}
}

// The hub tests above talk to the hub directly, which is how a real bug in the
// HTTP handler slipped through: frames reached subscribers but never reached a
// browser. This drives the actual endpoint over a real connection.
func TestViewerActuallyReceivesFramesOverHTTP(t *testing.T) {
	e := newTestEnv(t)
	tok := e.login("admin@x.com", "adminpassword")
	key := e.newDeviceKey(t, "tablet-1")

	srv := httptest.NewServer(e.mux)
	defer srv.Close()

	// Attach a viewer.
	req, _ := http.NewRequest("GET", srv.URL+"/api/v1/devices/tablet-1/stream.mjpeg", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("viewer connect: %v", err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "multipart/x-mixed-replace") {
		t.Fatalf("Content-Type = %q", ct)
	}

	got := make(chan []byte, 4)
	go func() {
		buf := make([]byte, 32*1024)
		var acc []byte
		for {
			n, err := resp.Body.Read(buf)
			if n > 0 {
				acc = append(acc, buf[:n]...)
				for {
					const hdr = "\r\n\r\n"
					i := strings.Index(string(acc), hdr)
					if i < 0 {
						break
					}
					var length int
					if _, e := fmt.Sscanf(string(acc), "--alimdmframe\r\nContent-Type: image/jpeg\r\nContent-Length: %d", &length); e != nil {
						return
					}
					start := i + len(hdr)
					if len(acc) < start+length {
						break
					}
					got <- append([]byte(nil), acc[start:start+length]...)
					acc = acc[start+length:]
					if len(acc) >= 2 {
						acc = acc[2:] // trailing CRLF
					}
				}
			}
			if err != nil {
				return
			}
		}
	}()

	// Give the handler a moment to subscribe before publishing.
	time.Sleep(200 * time.Millisecond)

	want := []byte("\xff\xd8 hello from the tablet \xff\xd9")
	fr, _ := http.NewRequest("POST", srv.URL+"/api/v1/devices/tablet-1/stream/frame", bytes.NewReader(want))
	fr.Header.Set("Authorization", "Bearer "+key)
	fresp, err := http.DefaultClient.Do(fr)
	if err != nil {
		t.Fatalf("frame post: %v", err)
	}
	fresp.Body.Close()

	select {
	case frame := <-got:
		if !bytes.Equal(frame, want) {
			t.Errorf("viewer got %q, want %q", frame, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no frame reached the viewer over HTTP — the browser would show a black panel")
	}
}

// The arming window must outlast a heartbeat. With a lease shorter than the
// 30s heartbeat, a requested stream expired before the tablet ever saw it and
// live view simply never started.
func TestArmingOutlastsAHeartbeat(t *testing.T) {
	if armLease <= 30*time.Second {
		t.Fatalf("armLease is %v, which is not longer than the 30s heartbeat: a requested stream can lapse unseen", armLease)
	}
	hub := &streamHub{subs: map[int64]chan []byte{}}
	hub.arm()
	// A viewer's shorter renewal must not cut the arming window short.
	hub.request()
	hub.mu.Lock()
	left := time.Until(hub.wantedTo)
	hub.mu.Unlock()
	if left < 30*time.Second {
		t.Errorf("after a viewer renewal only %v is left; the tablet could still miss it", left)
	}
}
