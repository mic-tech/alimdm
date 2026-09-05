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
	"strings"
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

// maxRelDepth bounds how deep an uploaded folder tree may be. A browser folder
// picker will happily hand over whatever is on disk, and nothing good comes of
// reproducing a forty-level tree inside a pupil's Downloads folder.
const maxRelDepth = 8

// sanitizeSegments splits a "/"-separated path and cleans each part with the
// same rules as a file name, dropping the empties. That collapses "a//b" and a
// leading "/" without special-casing either, and leaves nothing that could mean
// "the directory above" — Sanitize turns ".." into "".
func sanitizeSegments(p string) []string {
	p = strings.ReplaceAll(strings.TrimSpace(p), "\\", "/")
	out := []string{}
	for _, seg := range strings.Split(p, "/") {
		if clean := Sanitize(seg); clean != "" {
			out = append(out, clean)
		}
	}
	return out
}

// SanitizeRelPath reduces a browser-supplied folder path ("Juz30/Surah-078") to
// a safe one, or "" for the top of the library. It holds folders only — the
// file name is not part of it.
func SanitizeRelPath(p string) string {
	segs := sanitizeSegments(p)
	if len(segs) > maxRelDepth {
		segs = segs[:maxRelDepth]
	}
	return strings.Join(segs, "/")
}

// SanitizeStoredPath reduces a full key ("Juz30/CD1/Track 01.mp3") to a safe
// relative path, or "" if nothing usable is left.
//
// The depth cap applies to the folders in front of the name, never to the name
// itself: capping the whole path would silently drop the file name off a deep
// upload and leave a write aimed at a directory. A too-deep file lands
// shallower instead, which is a shape the operator can see and fix.
//
// It is idempotent, so a key sanitised on the way in still matches itself on
// the way back out of a URL.
func SanitizeStoredPath(p string) string {
	segs := sanitizeSegments(p)
	if len(segs) == 0 {
		return ""
	}
	name := segs[len(segs)-1]
	folders := segs[:len(segs)-1]
	if len(folders) > maxRelDepth {
		folders = folders[:maxRelDepth]
	}
	return strings.Join(append(folders, name), "/")
}

// resolve maps a caller-supplied relative path to a path inside the root.
//
// Sanitising each segment is what actually stops traversal; the containment
// check after it is the backstop that would catch a mistake in the sanitising,
// which is exactly when it matters most.
func (s *Store) resolve(rel string) (string, error) {
	clean := SanitizeStoredPath(rel)
	if clean == "" {
		return "", errors.New("invalid file name")
	}
	full := filepath.Join(s.root, filepath.FromSlash(clean))
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
//
// rel is the file's path inside the library, folders and all, and the folders
// are created to match: the library on disk is the shape the operator uploaded,
// which is the only version of it that stays obvious months later.
//
// The write goes to a temp file first so a failed or truncated upload cannot
// replace a good file that is already there.
func (s *Store) Ingest(r io.Reader, rel string, maxBytes int64) (sha256hex, path string, size int64, err error) {
	final, err := s.resolve(rel)
	if err != nil {
		return "", "", 0, err
	}
	dir := filepath.Dir(final)
	if err = os.MkdirAll(dir, 0o755); err != nil {
		return "", "", 0, err
	}
	// Staged in the destination directory, so the rename that follows is within
	// one filesystem and therefore atomic.
	dst, err := os.CreateTemp(dir, "upload-*")
	if err != nil {
		return "", "", 0, err
	}
	tmp := dst.Name()
	defer func() {
		dst.Close()
		if err != nil {
			os.Remove(tmp)
			s.pruneEmptyDirs(dir)
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

// pruneEmptyDirs walks back towards the root removing directories a delete just
// emptied, so clearing a folder does not leave its skeleton behind for ever. It
// stops at the root, and at the first directory that still holds something —
// os.Remove refuses a directory that is not empty, which is the whole check.
func (s *Store) pruneEmptyDirs(dir string) {
	absRoot, err := filepath.Abs(s.root)
	if err != nil {
		return
	}
	for {
		abs, err := filepath.Abs(dir)
		if err != nil || abs == absRoot ||
			!strings.HasPrefix(abs, absRoot+string(os.PathSeparator)) {
			return
		}
		if os.Remove(dir) != nil {
			return
		}
		dir = filepath.Dir(dir)
	}
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
	if err := os.Remove(full); err != nil {
		return err
	}
	s.pruneEmptyDirs(filepath.Dir(full))
	return nil
}
