package httpapi

// Asking a tablet what it has been doing.
//
// The console could see that a device was online and what it had been asked to
// do, and nothing at all about why any of it failed. Every diagnosis meant
// having the tablet in hand, over a cable, in the room it lives in — which for
// a fleet in a school is most of a morning for a question the device could have
// answered itself.
//
// It travels as a command, like everything else the console asks of a tablet:
// the device answers on its next check-in, and the answer is the command's
// result. Nothing new is stored, and nothing lands on disk beyond the row that
// was already there.
//
// Admin-only, both to ask and to read. A log carries what the device has been
// doing minute by minute; that is a level of detail about a classroom that
// belongs with whoever runs the fleet rather than with everyone who can sign in.

import (
	"encoding/json"
	"net/http"

	"ali-mdm/server/internal/store"
)

// requestDeviceLogs queues a log capture. The tablet answers on its next
// check-in, so the console polls for the result rather than waiting here.
func (s *Server) requestDeviceLogs(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	dev, err := s.st.GetDevice(id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "unknown device")
		return
	}
	if err := s.queueDeviceCommand(dev.ID, "get_logs", nil); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not ask for the log")
		return
	}
	s.record(r, "logs_requested", store.EventInfo, dev.ID,
		"Asked "+deviceLabel(dev.ID, dev.Name)+" for its log")
	writeJSON(w, map[string]any{"requested": true})
}

// deviceLogs returns the most recent capture: its state, and the log itself
// once the device has answered.
func (s *Server) deviceLogs(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.st.GetDevice(id); err != nil {
		writeErr(w, http.StatusNotFound, "unknown device")
		return
	}
	cmds, err := s.st.ListCommands(id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not read commands")
		return
	}
	// Never asked is not an error: the page offers the button.
	out := map[string]any{"status": "none"}

	// ListCommands is newest first, so the first match of each kind is the
	// latest one.
	var haveLog, haveCapture bool
	for _, c := range cmds {
		switch {
		case c.Type == "get_logs" && !haveLog:
			haveLog = true
			out["status"] = c.Status
			out["requested_at"] = c.CreatedAt
			out["error"] = c.ErrMsg
			if c.Result != "" {
				var parsed map[string]any
				if json.Unmarshal([]byte(c.Result), &parsed) == nil {
					out["app_log"] = parsed["app_log"]
					out["logcat"] = parsed["logcat"]
					out["state"] = parsed["state"]
					out["collected_at"] = parsed["collected_at"]
				}
			}

		case c.Type == "screenshot" && !haveCapture:
			// How the last screen capture went, which is the failure an
			// operator is usually chasing when they open this tab. Carried
			// here rather than left in the Commands list because the reason a
			// capture failed — an accessibility service that is off, a policy
			// that blocks it — is a fact about the device, not an incident.
			//
			// The newest attempt whatever its outcome: a success after a run
			// of failures is the thing worth knowing, and an old error shown
			// alone would send someone chasing a problem that has gone.
			haveCapture = true
			capture := map[string]any{"status": c.Status, "at": c.CreatedAt}
			if c.ErrMsg != "" {
				capture["error"] = c.ErrMsg
			}
			out["last_capture"] = capture
		}
		if haveLog && haveCapture {
			break
		}
	}
	writeJSON(w, out)
}
