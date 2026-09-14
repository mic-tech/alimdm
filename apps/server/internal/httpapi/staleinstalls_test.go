package httpapi

import (
	"testing"
	"time"

	"ali-mdm/server/internal/store"
)

// stageStaleInstall puts a sent install on the device, sent at sentAt ("" for
// a row from before sent_at was kept).
func (e *testEnv) stageStaleInstall(t *testing.T, id, cmd, pkg, sentAt string) {
	t.Helper()
	if err := e.st.EnqueueAPKUpdate(&store.APKUpdate{CommandID: cmd, DeviceID: id, PackageName: pkg, APKPath: pkg + ".apk", Status: "pending"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.ClaimPendingAPKUpdates(id); err != nil {
		t.Fatal(err)
	}
	if err := e.st.SetAPKUpdateSentAt(cmd, sentAt); err != nil {
		t.Fatal(err)
	}
}

func (e *testEnv) installStatus(t *testing.T, cmd string) (string, string) {
	t.Helper()
	st, msg, err := e.st.APKUpdateStatus(cmd)
	if err != nil {
		t.Fatal(err)
	}
	return st, msg
}

func TestStaleInstallsAreSettledFromTheAppList(t *testing.T) {
	e := newTestEnv(t)
	if err := e.st.UpsertGroup(&testGroup); err != nil {
		t.Fatal(err)
	}
	key := e.newDeviceKey(t, "tab-1")
	old := time.Now().UTC().Add(-5 * time.Hour).Format(time.RFC3339)
	e.stageStaleInstall(t, "tab-1", "apk-there", "com.there", old)
	e.stageStaleInstall(t, "tab-1", "apk-missing", "com.missing", old)
	e.stageStaleInstall(t, "tab-1", "apk-legacy", "com.legacy", "")
	e.stageStaleInstall(t, "tab-1", "apk-recent", "com.recent", time.Now().UTC().Format(time.RFC3339))

	// No inventory yet: the heartbeat asks for one rather than guessing.
	e.do("POST", "/api/v1/devices/tab-1/heartbeat", key, map[string]any{})
	if !e.st.HasRecentOpenCommand("tab-1", "list_apps", time.Now()) {
		t.Fatal("no list_apps queued for stale installs")
	}
	if st, _ := e.installStatus(t, "apk-there"); st != "sent" {
		t.Fatalf("settled without an app list: %s", st)
	}
	// Asked once, not on every heartbeat.
	e.do("POST", "/api/v1/devices/tab-1/heartbeat", key, map[string]any{})
	if n := len(e.deviceCommands(t, "tab-1", key)); n != 1 {
		t.Errorf("%d commands queued, want one list_apps", n)
	}

	e.reportApps(t, key, "tab-1", []map[string]any{
		{"package_name": "com.there"}, {"package_name": "com.legacy"},
	})
	for cmd, want := range map[string]string{
		"apk-there": "installed", "apk-legacy": "installed", "apk-missing": "failed",
		// Still inside its time to report by itself.
		"apk-recent": "sent",
	} {
		if st, msg := e.installStatus(t, cmd); st != want {
			t.Errorf("%s: %s (%s), want %s", cmd, st, msg, want)
		}
	}
}
