package store

// Matching a tablet to the enrolment QR it was provisioned from, for when the
// token inside that QR never reaches the app.
//
// Android hands the QR's admin extras to the app in a broadcast. Before Android
// 10 that broadcast is the only copy, and some vendor builds refuse to start an
// app for it: Lenovo's TB-X304F on 8.1 discards it because Ali MDM is not on
// its built-in auto-start list. What does survive is the APK download the QR
// caused, which reaches this server carrying the QR's nonce in its URL, the
// tablet's public IP, and a user agent naming its model and Android version.
// The app can later present that model and version from the same network and
// be enrolled as that QR asked, without the token ever passing through it.

import (
	"errors"
	"strings"
	"time"
)

// ProvisionClaim is what an enrolment QR asked for.
type ProvisionClaim struct {
	Nonce   string
	GroupID string
	Label   string
}

// ErrNoProvisionMatch: no download fits. ErrAmbiguousProvision: downloads from
// codes with different groups or labels fit, so which to use cannot be told.
var (
	ErrNoProvisionMatch   = errors.New("no matching provisioning download")
	ErrAmbiguousProvision = errors.New("more than one provisioning download matches")
)

// provisionRetention bounds both tables. A claim is only useful for minutes.
const provisionRetention = 24 * time.Hour

// CreateProvisionClaim records a newly generated QR.
func (s *Store) CreateProvisionClaim(c ProvisionClaim) error {
	s.pruneProvisioning()
	_, err := s.db.Exec(`INSERT INTO provision_claims(nonce, group_id, label, created_at) VALUES(?,?,?,?)`,
		c.Nonce, c.GroupID, c.Label, nowISO())
	return err
}

// RecordProvisionDownload notes that a complete APK was served for a QR. An
// unknown nonce is ignored: only this server's QRs can be claimed.
func (s *Store) RecordProvisionDownload(nonce, ip, userAgent string) error {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM provision_claims WHERE nonce=?`, nonce).Scan(&n); err != nil || n == 0 {
		return err
	}
	_, err := s.db.Exec(`INSERT INTO provision_downloads(nonce, ip, user_agent, at) VALUES(?,?,?,?)`,
		nonce, ip, userAgent, nowISO())
	return err
}

// ClaimProvisionDownload finds the download a tablet is asking about and marks
// it taken by deviceID. It must have come from ip within window, and its user
// agent must name exactly this model and Android version. A download already
// taken by the same device still matches, so a claim whose reply was lost can
// be repeated.
func (s *Store) ClaimProvisionDownload(ip, model, androidVersion, deviceID string, window time.Duration) (*ProvisionClaim, error) {
	if ip == "" || model == "" || androidVersion == "" {
		return nil, ErrNoProvisionMatch
	}
	since := time.Now().UTC().Add(-window).Format(time.RFC3339)
	rows, err := s.db.Query(
		`SELECT d.id, d.user_agent, d.claimed_by, c.nonce, c.group_id, c.label
		   FROM provision_downloads d JOIN provision_claims c ON c.nonce = d.nonce
		  WHERE d.ip = ? AND d.at >= ? AND (d.claimed_by = '' OR d.claimed_by = ?)
		  ORDER BY d.id DESC`, ip, since, deviceID)
	if err != nil {
		return nil, err
	}
	// The download manager's user agent reads
	// "AndroidDownloadManager/8.1.0 (Linux; U; Android 8.1.0; Lenovo TB-X304F Build/OPM1...)".
	wantVersion := "Android " + androidVersion + ";"
	wantModel := "; " + model + " Build/"
	// Newest first: the download a tablet is asking about is almost always the
	// latest from its network. Codes that ask for the same group and label are
	// interchangeable — a code regenerated after a failed setup leaves an
	// unclaimed download behind that differs only by nonce — so only codes
	// that disagree on either make the answer ambiguous.
	var matchID int64
	var match *ProvisionClaim
	ambiguous := false
	for rows.Next() {
		var id int64
		var ua, claimedBy string
		var c ProvisionClaim
		if err := rows.Scan(&id, &ua, &claimedBy, &c.Nonce, &c.GroupID, &c.Label); err != nil {
			rows.Close()
			return nil, err
		}
		if !strings.Contains(ua, wantVersion) || !strings.Contains(ua, wantModel) {
			continue
		}
		if match != nil && (c.GroupID != match.GroupID || c.Label != match.Label) {
			ambiguous = true
		}
		// The first seen is the newest; a download this device already holds
		// wins over it, so a repeated claim keeps its slot.
		if match == nil || (claimedBy == deviceID && deviceID != "") {
			cc := c
			match, matchID = &cc, id
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if match == nil {
		return nil, ErrNoProvisionMatch
	}
	if ambiguous {
		return nil, ErrAmbiguousProvision
	}
	res, err := s.db.Exec(`UPDATE provision_downloads SET claimed_by=? WHERE id=? AND (claimed_by='' OR claimed_by=?)`,
		deviceID, matchID, deviceID)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrNoProvisionMatch // taken by a concurrent claim
	}
	return match, nil
}

func (s *Store) pruneProvisioning() {
	cutoff := time.Now().UTC().Add(-provisionRetention).Format(time.RFC3339)
	_, _ = s.db.Exec(`DELETE FROM provision_downloads WHERE at < ?`, cutoff)
	_, _ = s.db.Exec(`DELETE FROM provision_claims WHERE created_at < ?`, cutoff)
}
