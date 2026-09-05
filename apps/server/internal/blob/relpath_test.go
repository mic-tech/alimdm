package blob

import (
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

// EnsureUnique is what stops one upload replacing another when two different
// folders fold to the same flat name.
func TestEnsureUniqueStepsAsideForATakenName(t *testing.T) {
	taken := map[string]bool{"Juz30 - CD1 - Track 01.mp3": true}
	got := EnsureUnique("Juz30 - CD1 - Track 01.mp3", func(n string) bool { return taken[n] })
	if got == "Juz30 - CD1 - Track 01.mp3" {
		t.Fatal("returned the taken name")
	}
	if !strings.HasSuffix(got, ".mp3") {
		t.Fatalf("%q lost its extension — the tablet decides what to do with a file by it", got)
	}
	if got != "Juz30 - CD1 - Track 01 (2).mp3" {
		t.Fatalf("got %q, want the counter before the extension", got)
	}
}

func TestEnsureUniqueLeavesAFreeNameAlone(t *testing.T) {
	got := EnsureUnique("notice.pdf", func(string) bool { return false })
	if got != "notice.pdf" {
		t.Fatalf("got %q, want notice.pdf", got)
	}
}

func TestEnsureUniqueKeepsCountingPastTheFirstClash(t *testing.T) {
	taken := map[string]bool{"a.mp3": true, "a (2).mp3": true, "a (3).mp3": true}
	if got := EnsureUnique("a.mp3", func(n string) bool { return taken[n] }); got != "a (4).mp3" {
		t.Fatalf("got %q, want a (4).mp3", got)
	}
}

// The result must stay inside the same limit Sanitize applies, or it would be
// truncated on the way to disk — straight back into the collision it avoided.
func TestEnsureUniqueStaysWithinTheNameLimit(t *testing.T) {
	long := strings.Repeat("x", maxNameLen-4) + ".mp3" // Sanitize trims this to the limit
	first := Sanitize(long)
	taken := map[string]bool{first: true}
	got := EnsureUnique(long, func(n string) bool { return taken[n] })
	if len(got) > maxNameLen {
		t.Fatalf("%d bytes, over the %d limit", len(got), maxNameLen)
	}
	if got == first {
		t.Fatal("returned the taken name")
	}
	if Sanitize(got) != got {
		t.Fatalf("%q would be changed again on the way to disk", got)
	}
}
