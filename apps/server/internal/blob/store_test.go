package blob

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Upload names are attacker-controlled, so nothing may resolve outside the root.
func TestNamesCannotEscapeTheRoot(t *testing.T) {
	root := t.TempDir()
	s := NewStore(filepath.Join(root, "files"))

	// A canary the traversal attempts would overwrite if they escaped.
	outside := filepath.Join(root, "secret.txt")
	if err := os.WriteFile(outside, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{
		"../secret.txt",
		"../../secret.txt",
		"..\\secret.txt",
		"subdir/../../secret.txt",
		"/etc/passwd",
	} {
		_, path, _, err := s.Ingest(strings.NewReader("attacker"), name, 1<<20)
		if err == nil {
			abs, _ := filepath.Abs(path)
			absRoot, _ := filepath.Abs(filepath.Join(root, "files"))
			if !strings.HasPrefix(abs, absRoot) {
				t.Errorf("%q was written outside the root, to %s", name, abs)
			}
		}
	}

	if b, _ := os.ReadFile(outside); string(b) != "original" {
		t.Fatalf("a traversal name overwrote a file outside the store: %q", b)
	}
}

func TestSanitize(t *testing.T) {
	cases := []struct{ in, want string }{
		{"worksheet.pdf", "worksheet.pdf"},
		{"  spaced.pdf  ", "spaced.pdf"},
		{"../../etc/passwd", "passwd"},
		{"sub/dir/notes.pdf", "notes.pdf"},
		{".hidden.pdf", "hidden.pdf"}, // a dotfile is invisible in the Files app
		{"..", ""},
		{".", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := Sanitize(c.in); got != c.want {
			t.Errorf("Sanitize(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	// Control characters and separators are dropped, not preserved.
	if got := Sanitize("a\x00b/c\nd.pdf"); strings.ContainsAny(got, "/\x00\n") {
		t.Errorf("Sanitize left a separator or control character: %q", got)
	}
	// A very long name is truncated but keeps its extension.
	long := strings.Repeat("x", 400) + ".pdf"
	got := Sanitize(long)
	if len(got) > maxNameLen {
		t.Errorf("long name not truncated: %d chars", len(got))
	}
	if !strings.HasSuffix(got, ".pdf") {
		t.Errorf("truncation dropped the extension: %q", got)
	}
}

// The size limit must stop an oversized upload, not merely report it after
// writing the whole thing to disk.
func TestOversizedUploadIsRejectedAndLeavesNothing(t *testing.T) {
	root := t.TempDir()
	s := NewStore(root)
	if _, _, _, err := s.Ingest(strings.NewReader(strings.Repeat("a", 5000)), "big.bin", 1000); err == nil {
		t.Fatal("an upload over the limit was accepted")
	}
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		t.Errorf("a rejected upload left %s behind", e.Name())
	}
}

// A failed upload must not clobber a good file that is already stored.
func TestFailedUploadLeavesTheExistingFileIntact(t *testing.T) {
	root := t.TempDir()
	s := NewStore(root)
	if _, _, _, err := s.Ingest(strings.NewReader("good content"), "notes.pdf", 1<<20); err != nil {
		t.Fatal(err)
	}
	// Same name, but over the limit: it must fail without touching the original.
	if _, _, _, err := s.Ingest(strings.NewReader(strings.Repeat("b", 5000)), "notes.pdf", 1000); err == nil {
		t.Fatal("oversized replacement was accepted")
	}
	f, err := s.Open("notes.pdf")
	if err != nil {
		t.Fatalf("the original file is gone: %v", err)
	}
	defer f.Close()
	buf := make([]byte, 32)
	n, _ := f.Read(buf)
	if string(buf[:n]) != "good content" {
		t.Errorf("original was corrupted: %q", buf[:n])
	}
}

func TestEmptyUploadIsRejected(t *testing.T) {
	s := NewStore(t.TempDir())
	if _, _, _, err := s.Ingest(strings.NewReader(""), "empty.pdf", 1<<20); err == nil {
		t.Error("an empty file was accepted")
	}
}

func TestRoundTripAndDelete(t *testing.T) {
	s := NewStore(t.TempDir())
	sha, _, size, err := s.Ingest(strings.NewReader("hello"), "greeting.txt", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if size != 5 {
		t.Errorf("size = %d, want 5", size)
	}
	// sha256("hello")
	if sha != "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824" {
		t.Errorf("sha256 = %s", sha)
	}
	if err := s.Delete("greeting.txt"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.Open("greeting.txt"); err == nil {
		t.Error("the file survived deletion")
	}
}
