package httpapi

// Remote control: taps, Back/Home and text, carried to the tablet on the reply
// to its next frame POST.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func (e *testEnv) sendInput(tok, id string, body map[string]any) (int, map[string]any) {
	e.t.Helper()
	rec, out := e.do("POST", "/api/v1/devices/"+id+"/input", tok, body)
	return rec.Code, out
}

// Input travels on the frame channel, so with nobody watching there is no
// channel — the event would sit in a queue until it was meaningless.
func TestInputNeedsALiveView(t *testing.T) {
	e := newTestEnv(t)
	tok := e.login("admin@x.com", "adminpassword")
	e.newDeviceKey(t, "tablet-1")

	if code, _ := e.sendInput(tok, "tablet-1", map[string]any{"type": "tap", "x": 0.5, "y": 0.5}); code != http.StatusConflict {
		t.Errorf("tap with no live view: status %d, want 409", code)
	}
}

// The round trip: an operator taps, and the tablet is told on its next frame.
func TestATapReachesTheTabletOnTheNextFrame(t *testing.T) {
	e := newTestEnv(t)
	tok := e.login("admin@x.com", "adminpassword")
	key := e.newDeviceKey(t, "tablet-1")

	if rec, _ := e.do("POST", "/api/v1/devices/tablet-1/stream/start", tok, nil); rec.Code != http.StatusOK {
		t.Fatalf("start stream: status %d", rec.Code)
	}
	if code, _ := e.sendInput(tok, "tablet-1", map[string]any{"type": "tap", "x": 0.25, "y": 0.75}); code != http.StatusOK {
		t.Fatalf("tap: status %d", code)
	}
	if code, _ := e.sendInput(tok, "tablet-1", map[string]any{"type": "key", "key": "back"}); code != http.StatusOK {
		t.Fatalf("key: status %d", code)
	}
	// Scrolling is a drag, and it is the direction of travel through the page
	// that is named, not the direction of the finger.
	for _, dir := range []string{"up", "down"} {
		if code, _ := e.sendInput(tok, "tablet-1", map[string]any{"type": "scroll", "dir": dir}); code != http.StatusOK {
			t.Errorf("scroll %q: status %d, want 200", dir, code)
		}
	}
	// Arrows and Enter drive a screen without a mouse; all of them are allowed.
	for _, k := range []string{"home", "enter", "up", "down", "left", "right"} {
		if code, _ := e.sendInput(tok, "tablet-1", map[string]any{"type": "key", "key": k}); code != http.StatusOK {
			t.Errorf("key %q: status %d, want 200", k, code)
		}
	}

	_, body := e.postFrame(t, "tablet-1", key, []byte("\xff\xd8jpeg"))
	in, _ := body["input"].([]any)
	if len(in) != 10 {
		t.Fatalf("the frame reply carried %d events, want 10", len(in))
	}
	first := in[0].(map[string]any)
	if first["type"] != "tap" || first["x"] != 0.25 || first["y"] != 0.75 {
		t.Errorf("first event = %v", first)
	}
	if in[1].(map[string]any)["key"] != "back" {
		t.Errorf("second event = %v", in[1])
	}

	// Delivered once: a tap replayed on the next frame would land twice.
	_, body = e.postFrame(t, "tablet-1", key, []byte("\xff\xd8jpeg"))
	if _, again := body["input"]; again {
		t.Error("the same input came back on the next frame; every tap would happen twice")
	}
}

// A screen is only worth tapping where it is, so a flood keeps the newest.
func TestAFloodOfInputKeepsTheNewest(t *testing.T) {
	e := newTestEnv(t)
	tok := e.login("admin@x.com", "adminpassword")
	key := e.newDeviceKey(t, "tablet-1")
	e.do("POST", "/api/v1/devices/tablet-1/stream/start", tok, nil)

	for i := 0; i < maxQueuedInput+5; i++ {
		e.sendInput(tok, "tablet-1", map[string]any{"type": "text", "text": string(rune('a' + i))})
	}
	_, body := e.postFrame(t, "tablet-1", key, []byte("\xff\xd8jpeg"))
	in, _ := body["input"].([]any)
	if len(in) != maxQueuedInput {
		t.Fatalf("queued %d events, want the cap of %d", len(in), maxQueuedInput)
	}
	// The last one sent must have survived; the first must not.
	last := in[len(in)-1].(map[string]any)["text"]
	if last != string(rune('a'+maxQueuedInput+4)) {
		t.Errorf("newest event is %v; the queue dropped the wrong end", last)
	}
}

func TestNonsenseInputIsRefused(t *testing.T) {
	e := newTestEnv(t)
	tok := e.login("admin@x.com", "adminpassword")
	e.newDeviceKey(t, "tablet-1")
	e.do("POST", "/api/v1/devices/tablet-1/stream/start", tok, nil)

	for _, bad := range []map[string]any{
		{"type": "tap", "x": 1.5, "y": 0.5},   // off the screen
		{"type": "tap", "x": 0.5, "y": -0.1},  // off the screen
		{"type": "key", "key": "power"},       // not one of ours: it changes the device, not the app on it
		{"type": "key", "key": ""},            // nothing pressed
		{"type": "text", "text": ""},          // nothing to type
		{"type": "scroll", "dir": "left"},     // scrolling sideways is not built
		{"type": "scroll"},                    // no direction
		{"type": "swipe", "x": 0.1, "y": 0.1}, // not built yet
	} {
		if code, _ := e.sendInput(tok, "tablet-1", bad); code != http.StatusBadRequest {
			t.Errorf("%v: status %d, want 400", bad, code)
		}
	}
}

// Taking control is worth a line in the feed. Every tap is not: a lesson's
// worth of them would bury everything else.
func TestTakingControlIsRecordedOnce(t *testing.T) {
	e := newTestEnv(t)
	tok := e.login("admin@x.com", "adminpassword")
	e.newDeviceKey(t, "tablet-1")
	e.do("POST", "/api/v1/devices/tablet-1/stream/start", tok, nil)

	for i := 0; i < 5; i++ {
		e.sendInput(tok, "tablet-1", map[string]any{"type": "tap", "x": 0.5, "y": 0.5})
	}

	rec, _ := e.do("GET", "/api/v1/events?device=tablet-1", tok, nil)
	var page struct {
		Events []struct {
			Kind    string `json:"kind"`
			Summary string `json:"summary"`
		} `json:"events"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, ev := range page.Events {
		if ev.Kind == "control_started" {
			n++
			if !strings.Contains(ev.Summary, "controlling") {
				t.Errorf("summary = %q", ev.Summary)
			}
		}
	}
	if n != 1 {
		t.Errorf("recorded %d control_started events for five taps, want 1", n)
	}
}
