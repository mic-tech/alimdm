// Package blob keeps operator-uploaded documents on disk.
//
// It is deliberately not the apk package. That one enforces an .apk extension,
// a 50KB floor, and only ever lists or deletes .apk files — all correct for
// installable packages and all wrong for a two-page worksheet. Sharing the type
// meant a PDF upload was rejected as "not an .apk file".
//
// Names come from an upload form, so every path is resolved against the root
// and checked before it is opened, written or removed.
package blob

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
)

type Store struct{ root string }

func NewStore(root string) *Store {
	os.MkdirAll(root, 0o755)
	return &Store{root: root}
}

// maxNameLen keeps a pathological upload name from hitting the filesystem's own
// limit, where the error would be far less obvious.
const maxNameLen = 120

// Sanitize reduces an uploaded name to a plain, safe file name. It strips any
// directory part, drops control characters and path separators, and refuses the
// names that mean something to the filesystem.
func Sanitize(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	name = strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == 0 || unicode.IsControl(r) {
			return -1
		}
		return r
	}, name)
	// A leading dot hides the file from the tablet's own Files app, which is
	// the opposite of the point.
	name = strings.TrimLeft(name, ".")
	if len(name) > maxNameLen {
		ext := filepath.Ext(name)
		if len(ext) > 16 {
			ext = ""
		}
		name = name[:maxNameLen-len(ext)] + ext
	}
	if name == "" || name == "." || name == ".." {
		return ""
	}
	return name
}

// EnsureUnique returns a variant of name that taken() does not claim.
//
// The library stores files flat, so a folder upload folds the folder path into
// the name to keep it unique. " - " is not a reserved character, though, so that
// fold is ambiguous: "Track 01.mp3" inside "Juz30/CD1" and "CD1 - Track 01.mp3"
// at the top of "Juz30" produce exactly the same name. Left alone the second
// upload replaces the first, which is the worst way to lose a file — no error,
// and a row that still looks right.
//
// What counts as taken is the caller's business; this only knows how to spell
// the alternative, and keeps it inside the same length limit Sanitize applies,
// so the answer cannot be truncated back into the collision it just avoided.
func EnsureUnique(name string, taken func(string) bool) string {
	name = Sanitize(name)
	if name == "" || !taken(name) {
		return name
	}
	ext := filepath.Ext(name)
	if len(ext) > 16 {
		ext = ""
	}
	stem := strings.TrimSuffix(name, ext)
	for n := 2; n < 1000; n++ {
		candidate := fitName(stem, fmt.Sprintf(" (%d)", n), ext)
		if !taken(candidate) {
			return candidate
		}
	}
	// A thousand names that all flatten together is not a real library; take
	// something that will not collide rather than looping or overwriting.
	return fitName(stem, " ("+strconv.FormatInt(time.Now().UnixNano(), 36)+")", ext)
}

// fitName joins the parts, trimming the stem on a rune boundary if the whole
// would run past the name limit.
func fitName(stem, suffix, ext string) string {
	r := []rune(stem)
	for len(string(r))+len(suffix)+len(ext) > maxNameLen && len(r) > 0 {
		r = r[:len(r)-1]
	}
	return string(r) + suffix + ext
}

// maxRelDepth bounds how deep an uploaded folder tree may be. A browser folder
// picker will happily hand over whatever is on disk, and nothing good comes of
// reproducing a forty-level tree inside a pupil's Downloads folder.
const maxRelDepth = 8

// SanitizeRelPath reduces a browser-supplied relative path ("Juz30/Surah-078")
// to a safe folder path, or "" for the top of the inbox.
//
// Storage on the server stays flat — this never touches where bytes land, only
// what the tablet is told to rebuild. That is deliberate: the traversal defence
// in resolve() keeps working untouched, and a malicious rel_path can at worst
// produce an odd-looking folder on a tablet, never a write outside the root.
//
// Each segment goes through the same Sanitize as a file name, so "..", control
// characters and separators cannot survive; empty segments are dropped, which
// collapses "a//b" and a leading "/" without special-casing either.
func SanitizeRelPath(p string) string {
	p = strings.ReplaceAll(strings.TrimSpace(p), "\\", "/")
	out := make([]string, 0, maxRelDepth)
	for _, seg := range strings.Split(p, "/") {
		clean := Sanitize(seg)
		if clean == "" {
			continue
		}
		out = append(out, clean)
		if len(out) == maxRelDepth {
			break
		}
	}
	return strings.Join(out, "/")
}

// resolve maps a caller-supplied name to a path inside the root, or errors.
func (s *Store) resolve(name string) (string, error) {
	base := Sanitize(name)
	if base == "" {
		return "", errors.New("invalid file name")
	}
	full := filepath.Join(s.root, base)
	absRoot, err := filepath.Abs(s.root)
	if err != nil {
		return "", err
	}
	absFull, err := filepath.Abs(full)
	if err != nil {
		return "", err
	}
	if absFull != absRoot && !strings.HasPrefix(absFull, absRoot+string(os.PathSeparator)) {
		return "", os.ErrPermission
	}
	return full, nil
}

// Ingest streams an upload to disk and returns its SHA-256 and final path.
// The write goes to a temp file first so a failed or truncated upload cannot
// replace a good file that is already there.
func (s *Store) Ingest(r io.Reader, name string, maxBytes int64) (sha256hex, path string, size int64, err error) {
	final, err := s.resolve(name)
	if err != nil {
		return "", "", 0, err
	}
	dst, err := os.CreateTemp(s.root, "upload-*")
	if err != nil {
		return "", "", 0, err
	}
	tmp := dst.Name()
	defer func() {
		dst.Close()
		if err != nil {
			os.Remove(tmp)
		}
	}()

	h := sha256.New()
	// One byte past the limit is enough to know it was exceeded, without
	// writing the whole of an oversized upload to disk first.
	size, err = io.Copy(io.MultiWriter(dst, h), io.LimitReader(r, maxBytes+1))
	if err != nil {
		return "", "", 0, err
	}
	if size > maxBytes {
		err = fmt.Errorf("file is larger than the %d MB limit", maxBytes>>20)
		return "", "", 0, err
	}
	if size == 0 {
		err = errors.New("file is empty")
		return "", "", 0, err
	}
	if err = dst.Close(); err != nil {
		return "", "", 0, err
	}
	if err = os.Rename(tmp, final); err != nil {
		return "", "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), final, size, nil
}

func (s *Store) Open(name string) (*os.File, error) {
	full, err := s.resolve(name)
	if err != nil {
		return nil, err
	}
	return os.Open(full)
}

func (s *Store) Delete(name string) error {
	full, err := s.resolve(name)
	if err != nil {
		return err
	}
	return os.Remove(full)
}
