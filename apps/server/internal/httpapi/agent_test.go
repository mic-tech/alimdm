package httpapi

import (
	"testing"

	"ali-mdm/server/internal/store"
)

// A tablet that goes offline mid-update must still get a later rollout when it
// returns. Its row is left mid-flight ("installing"), and if a new rollout did
// not reset that, the tablet would come back and be offered nothing.
func TestOfflineTabletPicksUpALaterRollout(t *testing.T) {
	e := newTestEnv(t)
	if err := e.st.UpsertGroup(&testGroup); err != nil {
		t.Fatal(err)
	}
	key := e.newDeviceKey(t, "tablet-away")

	// It was mid-install when it vanished.
	if err := e.st.SaveAgentRelease(&store.AgentRelease{
		VersionCode: 54, VersionName: "1.2.29", FileName: "a.apk", SHA256: "x", Size: 1,
	}); err != nil {
		t.Fatal(err)
	}
	_ = e.st.QueueAgentUpdate("tablet-away", 54)
	_ = e.st.SetAgentUpdateStatus("tablet-away", store.AgentInstalling, "")

	// A newer build is staged and rolled out while the tablet is still away.
	if err := e.st.SaveAgentRelease(&store.AgentRelease{
		VersionCode: 55, VersionName: "1.2.30", FileName: "b.apk", SHA256: "y", Size: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := e.st.QueueAgentUpdate("tablet-away", 55); err != nil {
		t.Fatal(err)
	}

	// It comes back much later and heartbeats.
	_, body := e.do("POST", "/api/v1/devices/tablet-away/heartbeat", key, map[string]any{})
	offer, ok := body["agent_update"].(map[string]any)
	if !ok || offer == nil {
		t.Fatalf("a returning tablet was offered no update: %v", body["agent_update"])
	}
	if got := offer["version_code"]; got != float64(55) {
		t.Errorf("offered version %v, want 55", got)
	}
	if got := offer["attempt"]; got != float64(1) {
		t.Errorf("attempt = %v, want 1 — the stale attempt count was not reset", got)
	}
}

// The trap in the same code path: a queued update whose target no longer
// matches the staged release is silently dropped. Staging a new build without
// rolling it out therefore strands whoever was queued for the old one.
func TestQueuedUpdateIsInertWhenTheReleaseMovedOn(t *testing.T) {
	e := newTestEnv(t)
	if err := e.st.UpsertGroup(&testGroup); err != nil {
		t.Fatal(err)
	}
	key := e.newDeviceKey(t, "tablet-1")

	_ = e.st.SaveAgentRelease(&store.AgentRelease{VersionCode: 54, VersionName: "1.2.29", FileName: "a.apk", SHA256: "x", Size: 1})
	_ = e.st.QueueAgentUpdate("tablet-1", 54)
	// A newer release is staged, but nobody re-rolls it out.
	_ = e.st.SaveAgentRelease(&store.AgentRelease{VersionCode: 55, VersionName: "1.2.30", FileName: "b.apk", SHA256: "y", Size: 1})

	_, body := e.do("POST", "/api/v1/devices/tablet-1/heartbeat", key, map[string]any{})
	if body["agent_update"] != nil {
		t.Errorf("expected no offer once the release moved on, got %v", body["agent_update"])
	}
}
