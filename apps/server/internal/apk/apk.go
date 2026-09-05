package apk

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Store keeps APKs on plain disk under root and records metadata.
type Store struct {
	root string
}

func NewStore(root string) *Store {
	os.MkdirAll(root, 0o755)
	return &Store{root: root}
}

// Ingest reads an uploaded APK, verifies it is non-trivial, computes its
// SHA-256, and stores it on disk. Returns the sha256 and final path.
func (s *Store) Ingest(r io.Reader, name string) (sha256hex, path string, size int64, err error) {
	if name == "" {
		name = "app.apk"
	}
	if filepath.Ext(name) != ".apk" {
		err = fmt.Errorf("not an .apk file: %s", name)
		return
	}
	dst, cerr := os.CreateTemp(s.root, "upload-*.apk")
	if cerr != nil {
		err = cerr
		return
	}
	defer dst.Close()
	h := sha256.New()
	size, cerr = io.Copy(io.MultiWriter(dst, h), r)
	if cerr != nil {
		os.Remove(dst.Name())
		err = cerr
		return
	}
	if size < 50*1024 { // mirror Ali MDM's 50KB minimum sanity check
		os.Remove(dst.Name())
		err = fmt.Errorf("apk too small (%d bytes) — likely corrupt", size)
		return
	}
	final := filepath.Join(s.root, sanitize(name))
	if rerr := os.Rename(dst.Name(), final); rerr != nil {
		os.Remove(dst.Name())
		err = rerr
		return
	}
	sha256hex = hex.EncodeToString(h.Sum(nil))
	path = final
	return
}

func sanitize(name string) string {
	name = filepath.Base(name) // strip any path traversal
	if name == "" || name == "." || name == ".." {
		name = "app.apk"
	}
	return name
}

// Open resolves a (possibly bare) APK name against the store root and opens it.
// It rejects path traversal so callers can pass user-supplied names safely.
func (s *Store) Open(name string) (*os.File, error) {
	base := sanitize(name)
	full := filepath.Join(s.root, base)
	// Defense in depth: ensure the resolved path stays inside the root.
	absRoot, _ := filepath.Abs(s.root)
	absFull, _ := filepath.Abs(full)
	if !strings.HasPrefix(absFull, absRoot+string(os.PathSeparator)) {
		return nil, os.ErrPermission
	}
	return os.Open(full)
}

// Delete removes an APK from the store. It applies the same traversal guards as
// Open, so a caller may pass a user-supplied name directly.
func (s *Store) Delete(name string) error {
	base := sanitize(name)
	full := filepath.Join(s.root, base)
	absRoot, _ := filepath.Abs(s.root)
	absFull, _ := filepath.Abs(full)
	if !strings.HasPrefix(absFull, absRoot+string(os.PathSeparator)) {
		return os.ErrPermission
	}
	// Only ever remove APKs, never anything else that happens to sit in the root.
	if !strings.HasSuffix(base, ".apk") {
		return os.ErrPermission
	}
	return os.Remove(full)
}

// List returns the base names of all .apk files in the store root. Used to
// match a managed app's package name against an uploaded APK so the server can
// auto-queue installs on enrollment.
func (s *Store) List() ([]string, error) {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasSuffix(e.Name(), ".apk") {
			names = append(names, e.Name())
		}
	}
	return names, nil
}
