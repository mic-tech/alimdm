package httpapi

// Setup-wizard QR provisioning.
//
// The tablet's setup wizard scans a QR, downloads the APK from this server,
// checks it against a signing-certificate digest, installs it as Device Owner
// and hands it the enrolment token. No Google relationship is involved: that is
// only required for zero-touch enrolment, where devices are registered by the
// reseller at purchase. This flow works with a self-signed APK on your own host.
//
// The payload has to be a plain JSON object of android.app.extra.PROVISIONING_*
// keys. Anything else — an androidenterprise:// URI, for instance — is simply
// not recognised, and a wizard scanning it appears to do nothing at all.

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
)

// env mirrors cmd/api's lookup: ALIMDM_<name> with a fallback to the older
// FK_<name>, so a container started with the previous environment keeps working.
func env(name string) string {
	if v := strings.TrimSpace(os.Getenv("ALIMDM_" + name)); v != "" {
		return v
	}
	return strings.TrimSpace(os.Getenv("FK_" + name))
}

// provisionQR returns the QR payload plus everything the console needs to
// explain what is missing when it cannot be used yet.
//
// Optional query params:
//
//	group=<id>   enrol scanned devices straight into a policy group
//	ssid=, wifi_password=   join Wi-Fi during provisioning
func (s *Server) provisionQR(w http.ResponseWriter, r *http.Request) {
	var problems []string

	// The staged release is the only thing a scan can install, so its absence
	// stops the QR here rather than at the tablet's setup wizard.
	staged := ""
	if rel, err := s.st.GetAgentRelease(); err == nil && rel.VersionCode > 0 {
		staged = rel.VersionName
	}
	if staged == "" {
		problems = append(problems, "No build has been staged. Upload the Ali MDM APK on the App update page — that is what a scanned tablet installs.")
	}

	// The checksum is the SHA-256 of the *signing certificate*, url-safe base64
	// without padding — not a hash of the APK file, which is a different field
	// and fails only after the wizard has downloaded everything. Supplied by
	// configuration rather than derived here: reading it would mean parsing the
	// APK Signature Scheme v2 block, and a wrong answer is worse than an absent
	// one because it fails late and looks like a network fault.
	checksum := strings.TrimSpace(env("PROVISION_CHECKSUM"))
	if checksum == "" {
		problems = append(problems, "No signing-certificate checksum configured. Set ALIMDM_PROVISION_CHECKSUM (apksigner verify --print-certs <apk>, SHA-256 digest, as url-safe base64 without padding).")
	}

	apkURL := s.baseURL + "/api/v1/provision/apk"
	if !strings.HasPrefix(strings.ToLower(apkURL), "https://") {
		// Some Android builds refuse a plain-HTTP download for device-owner
		// provisioning. It often works on a trusted LAN, so this is a warning.
		problems = append(problems, "The APK download URL is not HTTPS ("+apkURL+"). Some Android versions reject non-HTTPS provisioning downloads.")
	}

	extras := map[string]any{
		"enroll_token": s.enrollToken,
		"cloud_url":    strings.TrimRight(s.baseURL, "/"),
		"org_id":       env("ORG_ID"),
	}
	if g := r.URL.Query().Get("group"); g != "" {
		if _, err := s.st.GetGroup(g); err != nil {
			http.Error(w, "unknown group", http.StatusBadRequest)
			return
		}
		extras["group_id"] = g
	}
	// Naming the tablet as it provisions. One code carries one label, so a
	// per-tablet name means generating a code per tablet; a shared code simply
	// leaves them unnamed, showing as their ids until someone renames them.
	if l := strings.TrimSpace(r.URL.Query().Get("label")); l != "" {
		if len(l) > 64 {
			l = l[:64]
		}
		extras["device_label"] = l
	}

	payload := map[string]any{
		"android.app.extra.PROVISIONING_DEVICE_ADMIN_COMPONENT_NAME":            "com.alimdm/.DeviceAdminReceiver",
		"android.app.extra.PROVISIONING_DEVICE_ADMIN_PACKAGE_DOWNLOAD_LOCATION": apkURL,
		"android.app.extra.PROVISIONING_DEVICE_ADMIN_SIGNATURE_CHECKSUM":        checksum,
		"android.app.extra.PROVISIONING_ADMIN_EXTRAS_BUNDLE":                    extras,
		// These tablets hold nothing at provisioning time, and requiring
		// encryption adds a reboot to every enrolment.
		"android.app.extra.PROVISIONING_SKIP_ENCRYPTION":               true,
		"android.app.extra.PROVISIONING_LEAVE_ALL_SYSTEM_APPS_ENABLED": true,
	}
	if ssid := r.URL.Query().Get("ssid"); ssid != "" {
		payload["android.app.extra.PROVISIONING_WIFI_SSID"] = ssid
		if pw := r.URL.Query().Get("wifi_password"); pw != "" {
			payload["android.app.extra.PROVISIONING_WIFI_PASSWORD"] = pw
			payload["android.app.extra.PROVISIONING_WIFI_SECURITY_TYPE"] = "WPA"
		}
	}

	// Compact, because every extra byte makes the QR denser to scan.
	encoded, err := json.Marshal(payload)
	if err != nil {
		http.Error(w, "could not build payload", http.StatusInternalServerError)
		return
	}

	writeJSON(w, map[string]any{
		"payload":  string(encoded),
		"apk_url":  apkURL,
		"checksum": checksum,
		"ready":    len(problems) == 0,
		"problems": problems,
		// What a tablet scanning this will actually install. Worth saying out
		// loud: it used to be a file on the server nobody had touched in days,
		// so a new tablet could arrive nine versions behind the fleet with
		// nothing on screen to suggest it.
		"build": staged,
	})
}
