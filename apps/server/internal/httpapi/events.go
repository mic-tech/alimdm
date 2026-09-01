package httpapi

// The console's notification feed.
//
// Everything an operator does to the fleet, and everything the server notices
// on its own, lands here. It exists because the console had no memory: an
// action produced a toast that vanished on the next render, so there was no way
// to answer "who changed the policy?" or "when did that tablet go quiet?" — and
// with more than one operator, no way to see anything you did not do yourself.
//
// Two rules keep it useful rather than noisy:
//
//   - Only state changes are recorded. Heartbeats, list calls and page views
//     are not events; at a 30s heartbeat one tablet alone would bury the feed.
//   - Recording never fails the action. A logging error is logged, not
//     returned: deleting a device must not fail because the feed is unwritable.

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"

	"ali-mdm/server/internal/store"
)

// record appends an event attributed to whoever is making the request. Errors
// are swallowed by design — see the note above.
func (s *Server) record(r *http.Request, kind, severity, deviceID, summary string) {
	actor := "system"
	if op := operatorFrom(r.Context()); op != nil {
		if op.Name != "" {
			actor = op.Name
		} else {
			actor = op.Email
		}
	} else if d := deviceFrom(r.Context()); d != nil {
		actor = "device"
	}
	s.recordAs(actor, kind, severity, deviceID, summary)
}

// recordAs is for events with no request behind them, such as the offline
// watcher noticing a tablet has gone quiet.
func (s *Server) recordAs(actor, kind, severity, deviceID, summary string) {
	if err := s.st.RecordEvent(&store.Event{
		Kind: kind, Severity: severity, Actor: actor,
		DeviceID: deviceID, Summary: summary,
	}); err != nil {
		log.Printf("events: could not record %s: %v", kind, err)
	}
}

// listEvents returns the recent feed plus this operator's unread count.
func (s *Server) listEvents(w http.ResponseWriter, r *http.Request) {
	op := operatorFrom(r.Context())
	if op == nil {
		writeErr(w, http.StatusUnauthorized, "not signed in")
		return
	}
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	events, err := s.st.ListEvents(limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not read events")
		return
	}
	marker := s.st.EventsReadMarker(op.Email)
	unread, err := s.st.CountEventsAfter(marker)
	if err != nil {
		unread = 0
	}
	writeJSON(w, map[string]any{
		"events": events,
		"unread": unread,
	})
}

// markEventsRead moves this operator's marker. The console sends the newest id
// it has actually rendered rather than "now", so events arriving between the
// read and the response are not silently marked seen.
func (s *Server) markEventsRead(w http.ResponseWriter, r *http.Request) {
	op := operatorFrom(r.Context())
	if op == nil {
		writeErr(w, http.StatusUnauthorized, "not signed in")
		return
	}
	var body struct {
		UpTo int64 `json:"up_to"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.UpTo <= 0 {
		latest, err := s.st.LatestEventID()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "could not read events")
			return
		}
		body.UpTo = latest
	}
	// Never move the marker backwards: a stale tab must not resurrect
	// notifications another tab has already cleared.
	if cur := s.st.EventsReadMarker(op.Email); body.UpTo < cur {
		body.UpTo = cur
	}
	if err := s.st.SetEventsReadMarker(op.Email, body.UpTo); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not save read marker")
		return
	}
	writeJSON(w, map[string]any{"ok": true, "read_up_to": body.UpTo})
}

// deviceLabel prefers the operator's label, falling back to the id, so a feed
// entry reads "Library tablet went quiet" rather than an opaque serial.
func deviceLabel(id, name string) string {
	if name != "" {
		return name
	}
	return id
}

// plural keeps counts in the feed readable: "1 device", "3 devices".
func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}
