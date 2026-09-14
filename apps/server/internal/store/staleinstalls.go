package store

// Installs a tablet took and never reported on.
//
// Until results were recorded, every install went pending, then sent, and
// stayed sent: the console still shows those as unfinished months later. And a
// tablet can still lose a result today — reset, unenrolled mid-download, or on
// a build whose report never arrived. The tablet's own app list is the one
// source that can settle them either way, so a sent install is judged against
// an inventory taken after it was sent: present means installed, absent means
// it did not happen.

import (
	"encoding/json"
	"time"
)

// StaleInstallAge is how long a sent install is given to report by itself. The
// slowest seen — 770MB on a Lenovo TB-X304F over school Wi-Fi — took most of
// an hour.
const StaleInstallAge = 3 * time.Hour

type staleInstall struct {
	commandID, pkg, version, sentAt string
}

func (s *Store) staleInstalls(deviceID string, now time.Time) ([]staleInstall, error) {
	cutoff := now.UTC().Add(-StaleInstallAge).Format(time.RFC3339)
	rows, err := s.db.Query(
		`SELECT command_id, package_name, version_name, sent_at FROM apk_updates
		  WHERE device_id=? AND status='sent' AND sent_at < ?`, deviceID, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []staleInstall
	for rows.Next() {
		var st staleInstall
		if err := rows.Scan(&st.commandID, &st.pkg, &st.version, &st.sentAt); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// StaleInstallsNeedInventory reports whether this device has installs past
// StaleInstallAge that its current app list is too old to judge.
func (s *Store) StaleInstallsNeedInventory(deviceID string, now time.Time) bool {
	stale, err := s.staleInstalls(deviceID, now)
	if err != nil || len(stale) == 0 {
		return false
	}
	_, fetchedAt := s.DeviceApps(deviceID)
	for _, st := range stale {
		if !inventoryCovers(fetchedAt, st.sentAt, now) {
			return true
		}
	}
	return false
}

func inventoryCovers(fetchedAt, sentAt string, now time.Time) bool {
	if fetchedAt == "" {
		return false
	}
	if sentAt == "" { // never stamped: only a list from the last StaleInstallAge will do
		sentAt = now.UTC().Add(-StaleInstallAge).Format(time.RFC3339)
	}
	return fetchedAt >= sentAt
}

// SettledInstall is one stale install decided from the inventory.
type SettledInstall struct {
	CommandID, Package string
	Installed          bool
	Legacy             bool // sent before sent_at was kept
}

// SettleStaleInstalls decides every stale install the current inventory can
// judge. A result the tablet reports later still overwrites the verdict.
func (s *Store) SettleStaleInstalls(deviceID string, now time.Time) ([]SettledInstall, error) {
	stale, err := s.staleInstalls(deviceID, now)
	if err != nil || len(stale) == 0 {
		return nil, err
	}
	entriesJSON, fetchedAt := s.DeviceApps(deviceID)
	var entries []struct {
		Package string `json:"package_name"`
		Version string `json:"version_name"`
	}
	_ = json.Unmarshal([]byte(entriesJSON), &entries)
	installed := map[string]string{}
	for _, e := range entries {
		installed[e.Package] = e.Version
	}
	var out []SettledInstall
	for _, st := range stale {
		if !inventoryCovers(fetchedAt, st.sentAt, now) {
			continue
		}
		have, ok := installed[st.pkg]
		// A version the row names has to be the version installed, or an update
		// that never landed would read as done on the strength of the old build.
		isIn := ok && (st.version == "" || have == "" || have == st.version)
		status, msg := "installed", ""
		if !isIn {
			status = "failed"
			msg = "No result was reported, and the tablet's app list does not include it"
			if ok {
				msg = "No result was reported, and the tablet still has version " + have
			}
		}
		if _, err := s.db.Exec(`UPDATE apk_updates SET status=?, last_error=? WHERE command_id=? AND status='sent'`,
			status, msg, st.commandID); err != nil {
			return out, err
		}
		out = append(out, SettledInstall{CommandID: st.commandID, Package: st.pkg, Installed: isIn, Legacy: st.sentAt == ""})
	}
	return out, nil
}

// HasRecentOpenCommand reports whether a command of this type was queued for
// the device within the last hour and has not finished, so a request is not
// queued again on every heartbeat while the first is on its way.
func (s *Store) HasRecentOpenCommand(deviceID, cmdType string, now time.Time) bool {
	since := now.UTC().Add(-time.Hour).Format(time.RFC3339)
	var n int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM commands WHERE device_id=? AND type=? AND status IN ('pending','sent') AND created_at >= ?`,
		deviceID, cmdType, since).Scan(&n)
	return n > 0
}

// APKUpdateStatus returns one install's status and recorded error.
func (s *Store) APKUpdateStatus(commandID string) (status, lastError string, err error) {
	err = s.db.QueryRow(`SELECT status, last_error FROM apk_updates WHERE command_id=?`, commandID).
		Scan(&status, &lastError)
	return status, lastError, err
}

// SetAPKUpdateSentAt overrides when an install was sent. For tests.
func (s *Store) SetAPKUpdateSentAt(commandID, sentAt string) error {
	_, err := s.db.Exec(`UPDATE apk_updates SET sent_at=? WHERE command_id=?`, sentAt, commandID)
	return err
}
