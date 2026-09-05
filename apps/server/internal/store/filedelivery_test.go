package store

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func newDeliveryTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "files.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// queueMany pushes n files at one device, the way sending a folder does.
func queueMany(t *testing.T, st *Store, deviceID string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if err := st.QueueFileDelivery(deviceID, fmt.Sprintf("track%03d.mp3", i)); err != nil {
			t.Fatal(err)
		}
	}
}

// A folder can hold hundreds of files, and the device downloads them one after
// another, reporting only as each finishes. Handing over the whole folder at
// once means everything behind an interrupted download is stranded — so a claim
// takes a batch and leaves the rest for the next check-in.
func TestAClaimIsBatched(t *testing.T) {
	st := newDeliveryTestStore(t)
	queueMany(t, st, "tablet-1", maxFileDeliveryBatch+40)

	first, err := st.ClaimPendingFileDeliveries("tablet-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != maxFileDeliveryBatch {
		t.Fatalf("first claim handed over %d files, want %d", len(first), maxFileDeliveryBatch)
	}
	if n := st.CountPendingFileDeliveries("tablet-1"); n != 40 {
		t.Fatalf("%d files still waiting, want 40", n)
	}
	// Every check-in takes another batch, never more.
	second, err := st.ClaimPendingFileDeliveries("tablet-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != maxFileDeliveryBatch {
		t.Fatalf("second claim handed over %d files, want %d", len(second), maxFileDeliveryBatch)
	}
	third, err := st.ClaimPendingFileDeliveries("tablet-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(third) != 15 {
		t.Fatalf("third claim handed over %d files, want the remaining 15", len(third))
	}
	// And nothing is handed out twice while the device is still working.
	if fourth, _ := st.ClaimPendingFileDeliveries("tablet-1"); len(fourth) != 0 {
		t.Fatalf("%d files were handed out again while still in flight", len(fourth))
	}
}

// A tablet rebooted mid-download leaves its claimed files marked sent and never
// reported. Without a way back they show as pending in the console for ever and
// are never retried.
func TestAClaimTheDeviceNeverReportsIsOfferedAgain(t *testing.T) {
	st := newDeliveryTestStore(t)
	queueMany(t, st, "tablet-1", 2)
	if _, err := st.ClaimPendingFileDeliveries("tablet-1"); err != nil {
		t.Fatal(err)
	}
	if n := st.CountPendingFileDeliveries("tablet-1"); n != 0 {
		t.Fatalf("%d files offered while the device is still working on them, want 0", n)
	}

	// The device goes away without reporting either file.
	stale := time.Now().UTC().Add(-staleSentAfter - time.Minute).Format(time.RFC3339)
	if _, err := st.db.Exec(`UPDATE file_deliveries SET updated_at=? WHERE device_id=?`,
		stale, "tablet-1"); err != nil {
		t.Fatal(err)
	}

	if n := st.CountPendingFileDeliveries("tablet-1"); n != 2 {
		t.Fatalf("%d files offered after the device went quiet, want 2 — the hint drives the fetch, so a count of 0 means they are never retried", n)
	}
	again, err := st.ClaimPendingFileDeliveries("tablet-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 2 {
		t.Fatalf("re-claim handed over %d files, want 2", len(again))
	}
}

// Retrying must not become a loop: a file the device cannot store is given a
// bounded number of goes and then left alone.
func TestRetriesAreStillCapped(t *testing.T) {
	st := newDeliveryTestStore(t)
	queueMany(t, st, "tablet-1", 1)
	stale := time.Now().UTC().Add(-staleSentAfter - time.Minute).Format(time.RFC3339)
	for i := 0; i < maxFileDeliveryAttempts+2; i++ {
		st.ClaimPendingFileDeliveries("tablet-1")
		if _, err := st.db.Exec(`UPDATE file_deliveries SET updated_at=? WHERE device_id=?`,
			stale, "tablet-1"); err != nil {
			t.Fatal(err)
		}
	}
	if n := st.CountPendingFileDeliveries("tablet-1"); n != 0 {
		t.Fatalf("still offering a file after %d attempts", maxFileDeliveryAttempts)
	}
}

// A delivery the device reported as done must never come back, however long ago
// it was claimed.
func TestADeliveredFileIsNotReOffered(t *testing.T) {
	st := newDeliveryTestStore(t)
	queueMany(t, st, "tablet-1", 1)
	st.ClaimPendingFileDeliveries("tablet-1")
	if err := st.SetFileDeliveryStatus("tablet-1", "track000.mp3", FileDeliveryDone, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`UPDATE file_deliveries SET updated_at=? WHERE device_id=?`,
		time.Now().UTC().Add(-48*time.Hour).Format(time.RFC3339), "tablet-1"); err != nil {
		t.Fatal(err)
	}
	if n := st.CountPendingFileDeliveries("tablet-1"); n != 0 {
		t.Fatalf("a delivered file was offered again")
	}
}
