package httpapi

// Authentication and authorisation on the routes that change something.
//
// Every case here failed before the audit that produced these tests, and the
// existing suite passed throughout — which is the point. A missing guard breaks
// nothing that anyone tests, because the legitimate client always sends the
// credential the server forgot to check.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"ali-mdm/server/internal/store"
)

// queueCommand asks the console to send a device a command and returns its id.
func queueCommand(t *testing.T, e *testEnv, tok, deviceID, kind string) string {
	t.Helper()
	rec, body := e.do("POST", "/api/v1/devices/"+deviceID+"/commands", tok,
		map[string]any{"type": kind, "params": map[string]any{}})
	if rec.Code != http.StatusOK {
		t.Fatalf("queue command: status %d (%s)", rec.Code, rec.Body.String())
	}
	id, _ := body["id"].(string)
	if id == "" {
		t.Fatalf("no command id in %v", body)
	}
	return id
}

// Reporting the outcome of a command writes to the database. It was reachable
// with no credential at all: anyone who could name a command id could mark it
// done and post arbitrary content as its result — including the body of a
// device log, which an operator then reads as fact.
func TestCommandResultNeedsADeviceKey(t *testing.T) {
	e := newTestEnv(t)
	adminTok := e.login("admin@x.com", "adminpassword")
	e.newDeviceKey(t, "tablet-1")
	id := queueCommand(t, e, adminTok, "tablet-1", "reboot")

	rec, _ := e.do("POST", "/api/v1/commands/"+id+"/result/", "",
		map[string]any{"status": "success", "result": "{}"})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous command result: status %d, want 401", rec.Code)
	}
}

// A valid key is not permission to speak for another tablet. The command id was
// the only thing identifying the row, so any enrolled device could close out
// another's work.
func TestOneDeviceCannotReportAnothersCommand(t *testing.T) {
	e := newTestEnv(t)
	adminTok := e.login("admin@x.com", "adminpassword")
	e.newDeviceKey(t, "tablet-1")
	otherKey := e.newDeviceKey(t, "tablet-2")
	id := queueCommand(t, e, adminTok, "tablet-1", "reboot")

	rec, _ := e.do("POST", "/api/v1/commands/"+id+"/result/", otherKey,
		map[string]any{"status": "success", "result": "{}"})
	if rec.Code == http.StatusOK {
		t.Fatal("tablet-2 closed a command belonging to tablet-1")
	}

	cmds, err := e.st.ListCommands("tablet-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(cmds) != 1 || cmds[0].Status == "success" {
		t.Fatalf("tablet-1's command reads %+v — another device changed it", cmds)
	}
}

// The package library is served under names that are just package ids, so an
// open route hands the fleet's software to anyone who guesses one.
func TestPackageDownloadNeedsADeviceKey(t *testing.T) {
	e := newTestEnv(t)
	rec, _ := e.do("GET", "/api/v1/apk/com.example.app.apk", "", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous package download: status %d, want 401", rec.Code)
	}
	rec, _ = e.do("GET", "/api/v1/apk/com.example.app/split_0.apk", "", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous split download: status %d, want 401", rec.Code)
	}
}

// The build a factory-fresh tablet installs has to stay open: it is fetched by
// the setup wizard, before the device has any credential to present. Guarding
// it would break enrolment, and it would gain nothing — the same bytes are
// served openly at /provision/apk by necessity.
func TestProvisioningBuildStaysOpen(t *testing.T) {
	e := newTestEnv(t)
	if rec, _ := e.do("GET", "/api/v1/provision/apk", "", nil); rec.Code == http.StatusUnauthorized {
		t.Fatal("provisioning APK now requires auth — a factory-fresh tablet cannot enrol")
	}
}

// Changing a password has to end the sessions the old one opened, or the advice
// everyone gives after a suspected theft does nothing for twelve hours.
func TestPasswordChangeEndsExistingSessions(t *testing.T) {
	e := newTestEnv(t)
	tok := e.login("admin@x.com", "adminpassword")
	if rec, _ := e.do("GET", "/api/v1/me", tok, nil); rec.Code != http.StatusOK {
		t.Fatalf("token rejected before the change: %d", rec.Code)
	}

	// Tokens carry a whole-second issued-at, so a change in the same second is
	// indistinguishable from one before it.
	time.Sleep(1100 * time.Millisecond)
	if rec, _ := e.do("POST", "/api/v1/me/password", tok, map[string]any{
		"current_password": "adminpassword", "new_password": "a-longer-password",
	}); rec.Code != http.StatusOK {
		t.Fatalf("password change: status %d", rec.Code)
	}

	if rec, _ := e.do("GET", "/api/v1/me", tok, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("the old token still works after a password change: status %d", rec.Code)
	}
	if fresh := e.login("admin@x.com", "a-longer-password"); fresh == "" {
		t.Fatal("could not sign in with the new password")
	}
}

// Unlimited guessing against a known email is the whole game. The lockout has
// to bite, and a correct password has to clear it.
func TestLoginLocksOutAfterRepeatedFailures(t *testing.T) {
	e := newTestEnv(t)
	var last int
	for i := 0; i < loginFreeAttempts+3; i++ {
		rec, _ := e.do("POST", "/api/v1/operator/login", "",
			map[string]any{"email": "admin@x.com", "password": "wrong"})
		last = rec.Code
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("after %d failures the answer is %d, want 429", loginFreeAttempts+3, last)
	}
	// Locked out even with the right password: the account, not the guess.
	rec, _ := e.do("POST", "/api/v1/operator/login", "",
		map[string]any{"email": "admin@x.com", "password": "adminpassword"})
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("lockout does not cover a correct password: status %d", rec.Code)
	}
}

// An unknown email used to return before any hashing happened, answering in
// microseconds where a real account cost ~50ms of PBKDF2 — a clean oracle for
// which addresses exist. Deliberately loose: this catches the difference
// between "does the work" and "returns immediately", not a subtle margin.
func TestLoginDoesNotLeakWhichEmailsExist(t *testing.T) {
	e := newTestEnv(t)
	known := timeLogin(e, "admin@x.com", "wrong-password")
	unknown := timeLogin(e, "nobody@x.com", "wrong-password")
	if unknown*4 < known {
		t.Fatalf("unknown email answers in %v against %v for a real one — that difference enumerates accounts",
			unknown, known)
	}
}

func timeLogin(e *testEnv, email, password string) time.Duration {
	start := time.Now()
	e.do("POST", "/api/v1/operator/login", "", map[string]any{"email": email, "password": password})
	return time.Since(start)
}

// The enrolment token is a shared secret compared on every enrolment attempt.
func TestEnrolRejectsAWrongToken(t *testing.T) {
	e := newTestEnv(t)
	rec, _ := e.do("POST", "/api/v1/devices/enroll", "", map[string]any{
		"token":       "not-the-token",
		"device_info": map[string]any{"serial_number": "abc"},
	})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("enrol with a bad token: status %d, want 401", rec.Code)
	}
}

// Admin-only routes must refuse an operator, and the check must be the live
// role rather than whatever the token claimed when it was minted.
func TestDemotionTakesEffectOnTheNextRequest(t *testing.T) {
	e := newTestEnv(t)
	adminTok := e.login("admin@x.com", "adminpassword")
	e.do("POST", "/api/v1/users", adminTok, map[string]string{
		"email": "two@x.com", "name": "Two", "role": "admin", "password": "adminpassword2",
	})
	twoTok := e.login("two@x.com", "adminpassword2")
	if rec, _ := e.do("GET", "/api/v1/users", twoTok, nil); rec.Code != http.StatusOK {
		t.Fatalf("admin cannot list users: %d", rec.Code)
	}

	if rec, _ := e.do("PUT", "/api/v1/users/two@x.com", adminTok,
		map[string]any{"role": "operator"}); rec.Code != http.StatusOK {
		t.Fatalf("demote: status %d", rec.Code)
	}
	if rec, _ := e.do("GET", "/api/v1/users", twoTok, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("a demoted admin still reaches user administration: status %d", rec.Code)
	}
}

// Guard against the shape of mistake this audit found: a device-facing route
// registered without its middleware.
func TestNoDeviceRouteIsLeftUnguarded(t *testing.T) {
	e := newTestEnv(t)
	e.newDeviceKey(t, "tablet-1")
	writes := []struct{ method, path string }{
		{"POST", "/api/v1/devices/tablet-1/heartbeat"},
		{"POST", "/api/v1/devices/tablet-1/apps"},
		{"POST", "/api/v1/devices/tablet-1/inbox"},
		{"POST", "/api/v1/devices/tablet-1/agent-update"},
		{"POST", "/api/v1/devices/tablet-1/stream/frame"},
		{"GET", "/api/v1/devices/tablet-1/commands"},
		{"GET", "/api/v1/devices/tablet-1/files"},
		{"GET", "/api/v1/devices/tablet-1/events"},
	}
	for _, w := range writes {
		rec, _ := e.do(w.method, w.path, "", map[string]any{})
		if rec.Code != http.StatusUnauthorized && rec.Code != http.StatusNotFound {
			t.Errorf("%s %s answers %d without a credential, want 401", w.method, w.path, rec.Code)
		}
	}
}

// Headers the whole console depends on, asserted here so a proxy change cannot
// quietly become the only thing setting them.
func TestSecurityHeadersAreSetByTheApp(t *testing.T) {
	e := newTestEnv(t)
	rec, _ := e.do("GET", "/healthz", "", nil)
	for header, want := range map[string]string{
		"X-Frame-Options":         "DENY",
		"Content-Security-Policy": "frame-ancestors 'none'",
		"X-Content-Type-Options":  "nosniff",
		"Referrer-Policy":         "strict-origin-when-cross-origin",
	} {
		if got := rec.Header().Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
	// HSTS only where it means something. Claiming it over plain HTTP is noise
	// browsers ignore, and the docs describe LAN setups that run that way.
	if got := rec.Header().Get("Strict-Transport-Security"); got != "" {
		t.Errorf("HSTS sent over plain HTTP: %q", got)
	}
	req := httptest.NewRequest("GET", "/healthz", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	rec2 := httptest.NewRecorder()
	e.mux.ServeHTTP(rec2, req)
	if rec2.Header().Get("Strict-Transport-Security") == "" {
		t.Error("no HSTS on a request the proxy says arrived over TLS")
	}
}

// An app install reports its outcome down the same endpoint as a command, but
// it is not a row in commands — it lives in apk_updates under a command_id of
// its own. Scoping the command update by device must not turn those reports
// into 404s, or every tablet retries an install report for ever.
func TestInstallResultsAreAccepted(t *testing.T) {
	e := newTestEnv(t)
	devKey := e.newDeviceKey(t, "tablet-1")
	if err := e.st.EnqueueAPKUpdate(&store.APKUpdate{
		CommandID: "apk-1", DeviceID: "tablet-1", PackageName: "com.example.app",
		VersionName: "1.0", APKPath: "com.example.app.apk", Status: "pending",
	}); err != nil {
		t.Fatal(err)
	}

	rec, _ := e.do("POST", "/api/v1/commands/apk-1/result/", devKey,
		map[string]any{"status": "success", "result": map[string]any{"installed_package": "com.example.app"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("install report: status %d (%s)", rec.Code, rec.Body.String())
	}

	// And it is recorded, which it never was before: rows used to sit at "sent"
	// for ever because nothing ever wrote a terminal status.
	ups, err := e.st.ClaimPendingAPKUpdates("tablet-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(ups) != 0 {
		t.Fatalf("the install is still pending after a success report: %+v", ups)
	}
}

// The same scoping still applies: one device cannot close another's install.
func TestOneDeviceCannotReportAnothersInstall(t *testing.T) {
	e := newTestEnv(t)
	e.newDeviceKey(t, "tablet-1")
	otherKey := e.newDeviceKey(t, "tablet-2")
	if err := e.st.EnqueueAPKUpdate(&store.APKUpdate{
		CommandID: "apk-2", DeviceID: "tablet-1", PackageName: "com.example.app",
		VersionName: "1.0", APKPath: "com.example.app.apk", Status: "pending",
	}); err != nil {
		t.Fatal(err)
	}
	if rec, _ := e.do("POST", "/api/v1/commands/apk-2/result/", otherKey,
		map[string]any{"status": "success"}); rec.Code == http.StatusOK {
		t.Fatal("tablet-2 closed an install queued for tablet-1")
	}
}

// A failed install was silent: the row read "failed" at best, nothing recorded
// why, and the console showed an app that simply never appeared. The reason the
// device sent is the only account of what happened.
func TestFailedInstallIsRecordedWithItsReason(t *testing.T) {
	e := newTestEnv(t)
	devKey := e.newDeviceKey(t, "tablet-1")
	if err := e.st.EnqueueAPKUpdate(&store.APKUpdate{
		CommandID: "apk-fail", DeviceID: "tablet-1", PackageName: "com.example.app",
		VersionName: "1.0", APKPath: "com.example.app.apk", Status: "pending",
	}); err != nil {
		t.Fatal(err)
	}

	// The device reporting a failure is still a successful report.
	rec, _ := e.do("POST", "/api/v1/commands/apk-fail/result/", devKey, map[string]any{
		"status": "error", "error_message": "INSTALL_FAILED_UPDATE_INCOMPATIBLE",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("failure report rejected: status %d (%s)", rec.Code, rec.Body.String())
	}

	events, err := e.st.ListEvents(10)
	if err != nil {
		t.Fatal(err)
	}
	var found *store.Event
	for i := range events {
		if events[i].Kind == "app_install_failed" {
			found = &events[i]
			break
		}
	}
	if found == nil {
		t.Fatal("no feed entry for a failed install — an operator has no way to learn of it")
	}
	if !strings.Contains(found.Summary, "com.example.app") {
		t.Errorf("entry does not name the package: %q", found.Summary)
	}
	if !strings.Contains(found.Summary, "INSTALL_FAILED_UPDATE_INCOMPATIBLE") {
		t.Errorf("entry drops the device's reason, which is the whole point: %q", found.Summary)
	}
	if found.Severity != store.EventError {
		t.Errorf("severity = %q, want error", found.Severity)
	}
}

// No reason given is still worth recording, and must not read as though the
// message were simply missing from the feed.
func TestFailedInstallWithNoReasonStillSaysSo(t *testing.T) {
	e := newTestEnv(t)
	devKey := e.newDeviceKey(t, "tablet-1")
	if err := e.st.EnqueueAPKUpdate(&store.APKUpdate{
		CommandID: "apk-quiet", DeviceID: "tablet-1", PackageName: "com.example.quiet",
		VersionName: "1.0", APKPath: "x.apk", Status: "pending",
	}); err != nil {
		t.Fatal(err)
	}
	e.do("POST", "/api/v1/commands/apk-quiet/result/", devKey, map[string]any{"status": "error"})
	events, _ := e.st.ListEvents(10)
	for _, ev := range events {
		if ev.Kind == "app_install_failed" {
			if !strings.Contains(ev.Summary, "no reason") {
				t.Errorf("silent failure reads %q", ev.Summary)
			}
			return
		}
	}
	t.Fatal("no entry for a reasonless failure")
}
