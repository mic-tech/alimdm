package store

import (
	"path/filepath"
	"testing"
)

func newAgentTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestAgentReleaseIsSingleRow(t *testing.T) {
	st := newAgentTestStore(t)

	if _, err := st.GetAgentRelease(); err == nil {
		t.Fatal("expected an error before any release is saved")
	}
	for _, r := range []*AgentRelease{
		{VersionCode: 45, VersionName: "1.2.20", FileName: "a.apk", SHA256: "aa", Size: 1},
		{VersionCode: 46, VersionName: "1.2.21", FileName: "b.apk", SHA256: "bb", Size: 2},
	} {
		if err := st.SaveAgentRelease(r); err != nil {
			t.Fatalf("save %d: %v", r.VersionCode, err)
		}
	}
	got, err := st.GetAgentRelease()
	if err != nil {
		t.Fatal(err)
	}
	// Uploading supersedes rather than accumulating: exactly one build is current.
	if got.VersionCode != 46 || got.FileName != "b.apk" {
		t.Fatalf("expected the newest release to win, got %+v", got)
	}
}

// Attempts must be counted by the server, not reported by the device, so a
// tablet stuck in a crash loop cannot hide how many times it has tried.
func TestAgentAttemptsIncrementOnlyWhenInstalling(t *testing.T) {
	st := newAgentTestStore(t)
	if err := st.QueueAgentUpdate("dev-1", 46); err != nil {
		t.Fatal(err)
	}
	u, err := st.GetAgentUpdate("dev-1")
	if err != nil {
		t.Fatal(err)
	}
	if u.Status != AgentQueued || u.Attempts != 0 {
		t.Fatalf("fresh rollout should be queued with 0 attempts, got %+v", u)
	}

	for i := 1; i <= 2; i++ {
		if err := st.SetAgentUpdateStatus("dev-1", AgentInstalling, ""); err != nil {
			t.Fatal(err)
		}
		u, _ = st.GetAgentUpdate("dev-1")
		if u.Attempts != i {
			t.Fatalf("after %d installing reports want attempts=%d, got %d", i, i, u.Attempts)
		}
	}

	// A terminal report records the outcome without spending another attempt.
	if err := st.SetAgentUpdateStatus("dev-1", AgentFailed, "boom"); err != nil {
		t.Fatal(err)
	}
	u, _ = st.GetAgentUpdate("dev-1")
	if u.Attempts != 2 {
		t.Fatalf("terminal report should not increment attempts, got %d", u.Attempts)
	}
	if u.Status != AgentFailed || u.LastError != "boom" {
		t.Fatalf("expected failed/boom, got %+v", u)
	}
}

// Re-triggering a rollout from the console must clear a previous failure,
// otherwise an operator retry would start already at the attempt cap.
func TestQueueAgentUpdateResetsAfterFailure(t *testing.T) {
	st := newAgentTestStore(t)
	if err := st.QueueAgentUpdate("dev-1", 46); err != nil {
		t.Fatal(err)
	}
	_ = st.SetAgentUpdateStatus("dev-1", AgentInstalling, "")
	_ = st.SetAgentUpdateStatus("dev-1", AgentFailed, "install rejected")

	if err := st.QueueAgentUpdate("dev-1", 47); err != nil {
		t.Fatal(err)
	}
	u, err := st.GetAgentUpdate("dev-1")
	if err != nil {
		t.Fatal(err)
	}
	if u.Status != AgentQueued || u.Attempts != 0 || u.LastError != "" {
		t.Fatalf("re-queue should reset attempts and error, got %+v", u)
	}
	if u.TargetVersionCode != 47 {
		t.Fatalf("expected new target 47, got %d", u.TargetVersionCode)
	}
}

func TestListAgentUpdatesEmptyIsNotAnError(t *testing.T) {
	st := newAgentTestStore(t)
	ups, err := st.ListAgentUpdates()
	if err != nil {
		t.Fatal(err)
	}
	if len(ups) != 0 {
		t.Fatalf("expected no rollouts, got %d", len(ups))
	}
}
