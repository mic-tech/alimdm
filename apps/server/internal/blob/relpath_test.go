package blob

import "testing"

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
