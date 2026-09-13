package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ali-mdm/server/internal/store"
)

const x304UA = "AndroidDownloadManager/8.1.0 (Linux; U; Android 8.1.0; Lenovo TB-X304F Build/OPM1.171019.026)"

// stageAgent puts a real file behind the staged release so /provision/apk serves it.
func (e *testEnv) stageAgent(t *testing.T, body []byte) {
	t.Helper()
	_, _, size, err := e.srv.agentAPKs.Ingest(bytes.NewReader(body), "agent.apk")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.st.SaveAgentRelease(&store.AgentRelease{
		VersionCode: 104, VersionName: "1.2.63", FileName: "agent.apk", SHA256: "x", Size: size,
	}); err != nil {
		t.Fatal(err)
	}
}

// qrDownloadURL generates a code and returns the download location inside it.
func (e *testEnv) qrDownloadURL(t *testing.T, tok, query string) string {
	t.Helper()
	_, body := e.do("GET", "/api/v1/provision/qr?"+query, tok, nil)
	var payload map[string]any
	if err := json.Unmarshal([]byte(body["payload"].(string)), &payload); err != nil {
		t.Fatal(err)
	}
	u, _ := payload["android.app.extra.PROVISIONING_DEVICE_ADMIN_PACKAGE_DOWNLOAD_LOCATION"].(string)
	if !strings.Contains(u, "?claim=") {
		t.Fatalf("download location %q carries no claim", u)
	}
	return strings.TrimPrefix(u, "http://x")
}

// download fetches the APK as the setup wizard would, through the proxy.
func (e *testEnv) download(t *testing.T, path, ip, ua string) {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	req.RemoteAddr = "127.0.0.1:40000"
	req.Header.Set("X-Forwarded-For", ip)
	req.Header.Set("User-Agent", ua)
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("download: status %d", rec.Code)
	}
}

func (e *testEnv) claim(t *testing.T, ip string, info map[string]any) (int, map[string]any) {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"device_info": info})
	req := httptest.NewRequest("POST", "/api/v1/provision/claim", bytes.NewReader(b))
	req.RemoteAddr = "127.0.0.1:40001"
	req.Header.Set("X-Forwarded-For", ip)
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req)
	out := map[string]any{}
	json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

var x304Info = map[string]any{"model": "Lenovo TB-X304F", "android_version": "8.1.0", "serial_number": "37f0432c"}

// The Lenovo 8.1 case: the token never reached the app, but the download did
// reach the server, and the tablet is enrolled with the QR's group and label.
func TestClaimEnrolsWithTheQRsGroupAndLabel(t *testing.T) {
	e := newTestEnv(t)
	tok := e.login("admin@x.com", "adminpassword")
	if err := e.st.UpsertGroup(&testGroup); err != nil {
		t.Fatal(err)
	}
	e.stageAgent(t, bytes.Repeat([]byte("apk bytes "), 8000))
	e.download(t, e.qrDownloadURL(t, tok, "group=default&label=Tab%20432C"), "69.243.127.164", x304UA)

	code, body := e.claim(t, "69.243.127.164", x304Info)
	if code != http.StatusOK || body["api_key"] == "" {
		t.Fatalf("claim: %d %v", code, body)
	}
	if _, has := body["token"]; has {
		t.Error("a claim must never hand back the enrolment token")
	}
	d, err := e.st.GetDevice("37f0432c")
	if err != nil {
		t.Fatal(err)
	}
	if d.GroupID != "default" || d.Name != "Tab 432C" {
		t.Errorf("enrolled as group=%q name=%q, want the QR's", d.GroupID, d.Name)
	}

	// Repeating it, as after a lost reply, still works for the same tablet...
	if code, _ := e.claim(t, "69.243.127.164", x304Info); code != http.StatusOK {
		t.Errorf("repeat claim by the same tablet: %d", code)
	}
	// ...but another tablet cannot take the same download.
	other := map[string]any{"model": "Lenovo TB-X304F", "android_version": "8.1.0", "serial_number": "ed9bde0a"}
	if code, _ := e.claim(t, "69.243.127.164", other); code != http.StatusNotFound {
		t.Errorf("second tablet claimed an already-claimed download: %d", code)
	}
}

func TestClaimRefusesWhatDoesNotMatch(t *testing.T) {
	e := newTestEnv(t)
	tok := e.login("admin@x.com", "adminpassword")
	e.stageAgent(t, bytes.Repeat([]byte("apk bytes "), 8000))
	e.download(t, e.qrDownloadURL(t, tok, ""), "69.243.127.164", x304UA)

	for name, tc := range map[string]struct {
		ip   string
		info map[string]any
	}{
		"another network": {"203.0.113.9", x304Info},
		"another model":   {"69.243.127.164", map[string]any{"model": "TB330FU", "android_version": "8.1.0", "serial_number": "a"}},
		"another version": {"69.243.127.164", map[string]any{"model": "Lenovo TB-X304F", "android_version": "15", "serial_number": "a"}},
		"nothing sent":    {"69.243.127.164", map[string]any{}},
	} {
		if code, body := e.claim(t, tc.ip, tc.info); code != http.StatusNotFound {
			t.Errorf("%s: status %d (%v), want 404", name, code, body)
		}
	}
	if n := len(mustDevices(t, e)); n != 0 {
		t.Errorf("%d devices enrolled by non-matching claims", n)
	}
}

// Two codes with different groups downloaded by the same model on one network
// cannot be told apart, so neither is guessed.
func TestClaimRefusesAmbiguousCodes(t *testing.T) {
	e := newTestEnv(t)
	tok := e.login("admin@x.com", "adminpassword")
	e.stageAgent(t, bytes.Repeat([]byte("apk bytes "), 8000))
	e.download(t, e.qrDownloadURL(t, tok, "label=A"), "69.243.127.164", x304UA)
	e.download(t, e.qrDownloadURL(t, tok, "label=B"), "69.243.127.164", x304UA)
	if code, _ := e.claim(t, "69.243.127.164", x304Info); code != http.StatusConflict {
		t.Errorf("status %d, want 409", code)
	}
}

// An interrupted download never became a tablet and is not claimable, and a
// download that did not come through one of this server's codes is not either.
func TestOnlyCompleteDownloadsOfRealCodesCount(t *testing.T) {
	e := newTestEnv(t)
	e.stageAgent(t, bytes.Repeat([]byte("apk bytes "), 8000))
	e.download(t, "/api/v1/provision/apk?claim=madeup", "69.243.127.164", x304UA)
	e.download(t, "/api/v1/provision/apk", "69.243.127.164", x304UA)
	if code, _ := e.claim(t, "69.243.127.164", x304Info); code != http.StatusNotFound {
		t.Errorf("status %d, want 404", code)
	}
}

// Headers from a client that is not the proxy are not believed.
func TestClientIPIgnoresForwardedHeadersFromPublicPeers(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "198.51.100.7:1234"
	req.Header.Set("X-Forwarded-For", "69.243.127.164")
	if got := clientIP(req); got != "198.51.100.7" {
		t.Errorf("clientIP = %s, want the peer", got)
	}
	req.RemoteAddr = "172.17.0.1:1234"
	req.Header.Set("X-Forwarded-For", "6.6.6.6, 69.243.127.164")
	if got := clientIP(req); got != "69.243.127.164" {
		t.Errorf("clientIP = %s, want the entry the proxy appended", got)
	}
}

func mustDevices(t *testing.T, e *testEnv) []store.Device {
	t.Helper()
	ds, err := e.st.ListDevices()
	if err != nil {
		t.Fatal(err)
	}
	return ds
}

// What happened on the second Lenovo: its setup was retried with a freshly
// generated code, leaving the first code's download unclaimed. Both asked for
// the same group and no label, so there is nothing to disambiguate.
func TestClaimTreatsEquivalentCodesAsOne(t *testing.T) {
	e := newTestEnv(t)
	tok := e.login("admin@x.com", "adminpassword")
	if err := e.st.UpsertGroup(&testGroup); err != nil {
		t.Fatal(err)
	}
	e.stageAgent(t, bytes.Repeat([]byte("apk bytes "), 8000))
	e.download(t, e.qrDownloadURL(t, tok, "group=default"), "69.243.127.164", x304UA)
	e.download(t, e.qrDownloadURL(t, tok, "group=default"), "69.243.127.164", x304UA)
	if code, body := e.claim(t, "69.243.127.164", x304Info); code != http.StatusOK {
		t.Fatalf("status %d (%v), want 200", code, body)
	}
	if d, err := e.st.GetDevice("37f0432c"); err != nil || d.GroupID != "default" {
		t.Errorf("device %v err %v, want enrolled into default", d, err)
	}
}
