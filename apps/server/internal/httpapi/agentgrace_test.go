package httpapi

import (
	"net/http"
	"testing"
	"time"

	"ali-mdm/server/internal/store"
)

func (e *testEnv) armAgentRollout(t *testing.T, id string) string {
	t.Helper()
	if err := e.st.UpsertGroup(&testGroup); err != nil {
		t.Fatal(err)
	}
	key := e.newDeviceKey(t, id)
	if err := e.st.SaveAgentRelease(&store.AgentRelease{
		VersionCode: 105, VersionName: "1.2.64", FileName: "a.apk", SHA256: "x", Size: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := e.st.QueueAgentUpdate(id, 105); err != nil {
		t.Fatal(err)
	}
	return key
}

func (e *testEnv) agentReport(t *testing.T, id, key, status, errText string) {
	t.Helper()
	rec, _ := e.do("POST", "/api/v1/devices/"+id+"/agent-update", key, map[string]any{
		"status": status, "error": errText, "version_code": 105,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("report %s: %d", status, rec.Code)
	}
}

func (e *testEnv) heartbeatOffer(t *testing.T, id, key string, running int) any {
	t.Helper()
	_, body := e.do("POST", "/api/v1/devices/"+id+"/heartbeat", key, map[string]any{
		"system": map[string]any{"app_version_code": running},
	})
	return body["agent_update"]
}

// The DE0A loop: a slow install is still running at the next heartbeat. The
// update must not be offered again, and the agent's reasonless "failed" must
// not end the rollout.
func TestSlowInstallIsNotRestartedOrFailed(t *testing.T) {
	e := newTestEnv(t)
	key := e.armAgentRollout(t, "tab-de0a")

	if e.heartbeatOffer(t, "tab-de0a", key, 104) == nil {
		t.Fatal("no offer before the first attempt")
	}
	e.agentReport(t, "tab-de0a", key, "installing", "")
	e.agentReport(t, "tab-de0a", key, "failed",
		"Still on versionCode 104 after attempting 105 (was 104): no reason reported by Android — the update may not have reached the installer")
	if offer := e.heartbeatOffer(t, "tab-de0a", key, 104); offer != nil {
		t.Errorf("offered again while the install is still in its grace: %v", offer)
	}
	up, _ := e.st.GetAgentUpdate("tab-de0a")
	if up.Status != store.AgentInstalling || up.Attempts != 1 {
		t.Errorf("status=%s attempts=%d, want installing after 1", up.Status, up.Attempts)
	}

	// The install lands; the tablet restarts on the new build with nothing
	// left to report, and its heartbeat closes the rollout.
	e.heartbeatOffer(t, "tab-de0a", key, 105)
	if up, _ := e.st.GetAgentUpdate("tab-de0a"); up.Status != store.AgentSuccess {
		t.Errorf("status=%s after heartbeating on the target build, want success", up.Status)
	}
}

// A failure Android gave a reason for is real and recorded straight away.
func TestInstallFailureWithAReasonIsRecorded(t *testing.T) {
	e := newTestEnv(t)
	key := e.armAgentRollout(t, "tab-1")
	e.agentReport(t, "tab-1", key, "installing", "")
	e.agentReport(t, "tab-1", key, "failed", "Still on versionCode 104 after attempting 105 (was 104): Blocked — Play Protect")
	if up, _ := e.st.GetAgentUpdate("tab-1"); up.Status != store.AgentFailed {
		t.Errorf("status=%s, want failed", up.Status)
	}
}

// Once the grace runs out without the tablet arriving on the new build, the
// update is offered again.
func TestUpdateIsReofferedAfterTheGrace(t *testing.T) {
	e := newTestEnv(t)
	key := e.armAgentRollout(t, "tab-1")
	e.agentReport(t, "tab-1", key, "installing", "")
	defer func(g time.Duration) { agentInstallGrace = g }(agentInstallGrace)
	agentInstallGrace = 0
	if e.heartbeatOffer(t, "tab-1", key, 104) == nil {
		t.Error("not offered again after the grace")
	}
}
