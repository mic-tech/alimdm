package blob

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// SanitizeRelPath is the only thing standing between a browser-supplied path
// and a folder name written on a tablet, so the traversal cases matter more
// than the happy ones.
func TestSanitizeRelPath(t *testing.T) {
	cases := []struct{ in, want string }{
		// The ordinary case: a folder picker's relative path.
		{"Juz30/Surah-078", "Juz30/Surah-078"},
		{"", ""},
		{"   ", ""},

		// Traversal, in the forms a hand-written request would use.
		{"../../etc", "etc"},
		{"..", ""},
		{"../..", ""},
		{"/etc/passwd", "etc/passwd"},
		{"a/../../b", "a/b"},

		// Windows separators, since the path comes from whatever machine the
		// operator is on.
		{`Juz30\Surah-078`, "Juz30/Surah-078"},

		// Empty and dot segments collapse rather than becoming "" or ".".
		{"a//b", "a/b"},
		{"./a/./b", "a/b"},

		// Hidden folders would be invisible in the tablet's Files app, which is
		// the opposite of the point.
		{".secret/audio", "secret/audio"},

		// Depth is bounded.
		{"a/b/c/d/e/f/g/h/i/j", "a/b/c/d/e/f/g/h"},
	}
	for _, c := range cases {
		if got := SanitizeRelPath(c.in); got != c.want {
			t.Errorf("SanitizeRelPath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// Whatever SanitizeRelPath returns must never escape the store root when it is
// joined to a file name and resolved.
func TestSanitizedPathStaysInsideRoot(t *testing.T) {
	s := NewStore(t.TempDir())
	for _, in := range []string{"../../etc", "/etc", `..\..\windows`, "a/../../b"} {
		rel := SanitizeRelPath(in)
		key := rel + " - track.mp3"
		if _, err := s.resolve(key); err != nil {
			t.Errorf("resolve(%q) from input %q errored: %v", key, in, err)
		}
	}
}

// The name is the last segment and must survive whatever the depth cap does to
// the folders: dropping it would leave a write aimed at a directory.
func TestStoredPathNeverLosesTheFileName(t *testing.T) {
	deep := strings.Repeat("folder/", maxRelDepth+5) + "track01.mp3"
	got := SanitizeStoredPath(deep)
	if !strings.HasSuffix(got, "/track01.mp3") {
		t.Fatalf("SanitizeStoredPath(%d folders deep) = %q, lost the file name", maxRelDepth+5, got)
	}
	if n := strings.Count(got, "/"); n != maxRelDepth {
		t.Fatalf("%q has %d folders, want the cap of %d", got, n, maxRelDepth)
	}
}

// A key sanitised on the way in has to match itself on the way back out of a
// URL, or a file could be stored and then never found again.
func TestStoredPathIsIdempotent(t *testing.T) {
	for _, in := range []string{
		"Juz30/CD1/Track 01.mp3", "notice.pdf", "../../etc/passwd",
		"a//b/c.mp3", strings.Repeat("d/", maxRelDepth+3) + "x.mp3",
	} {
		once := SanitizeStoredPath(in)
		if twice := SanitizeStoredPath(once); twice != once {
			t.Fatalf("SanitizeStoredPath(%q): %q then %q", in, once, twice)
		}
	}
}

func TestStoredPathRejectsWhatIsLeftOfNothing(t *testing.T) {
	for _, in := range []string{"", "   ", "/", "..", "../..", "./././"} {
		if got := SanitizeStoredPath(in); got != "" {
			t.Fatalf("SanitizeStoredPath(%q) = %q, want empty", in, got)
		}
	}
}

// The library on disk should be the shape that was uploaded.
func TestIngestBuildsTheFolders(t *testing.T) {
	root := t.TempDir()
	st := NewStore(root)
	if _, _, _, err := st.Ingest(strings.NewReader("aaa"), "Juz30/CD1/Track 01.mp3", 1<<20); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "Juz30", "CD1", "Track 01.mp3")); err != nil {
		t.Fatalf("not stored under its folders: %v", err)
	}
	// Same name, different folder: both survive, which is the whole point of
	// keeping the folders on disk.
	if _, _, _, err := st.Ingest(strings.NewReader("bbb"), "Juz30/CD2/Track 01.mp3", 1<<20); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(filepath.Join(root, "Juz30", "CD1", "Track 01.mp3"))
	if err != nil || string(first) != "aaa" {
		t.Fatalf("the first file was replaced: %q, %v", first, err)
	}
}

// A traversal attempt must land inside the root, whatever it claims.
func TestIngestCannotEscapeTheRoot(t *testing.T) {
	root := t.TempDir()
	st := NewStore(root)
	_, path, _, err := st.Ingest(strings.NewReader("x"), "../../etc/evil.mp3", 1<<20)
	if err != nil {
		return // refusing outright is fine too
	}
	abs, _ := filepath.Abs(path)
	absRoot, _ := filepath.Abs(root)
	if !strings.HasPrefix(abs, absRoot+string(os.PathSeparator)) {
		t.Fatalf("wrote to %q, outside %q", abs, absRoot)
	}
}

// Deleting the last file in a folder should take the folder with it, or the
// library fills up with the skeletons of folders that were emptied months ago.
func TestDeleteClearsFoldersItEmpties(t *testing.T) {
	root := t.TempDir()
	st := NewStore(root)
	st.Ingest(strings.NewReader("aaa"), "Juz30/CD1/Track 01.mp3", 1<<20)
	st.Ingest(strings.NewReader("bbb"), "Juz30/CD1/Track 02.mp3", 1<<20)

	if err := st.Delete("Juz30/CD1/Track 01.mp3"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "Juz30", "CD1")); err != nil {
		t.Fatal("removed a folder that still had a file in it")
	}
	if err := st.Delete("Juz30/CD1/Track 02.mp3"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "Juz30")); !os.IsNotExist(err) {
		t.Fatalf("empty folders were left behind: %v", err)
	}
	// And never the root itself.
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("the library root was removed: %v", err)
	}
}
