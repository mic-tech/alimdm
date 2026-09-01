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
