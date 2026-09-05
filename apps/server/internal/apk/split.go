package apk

// Split-APK archives.
//
// A modern Android app often ships as several APKs rather than one: a base,
// plus configuration splits for the device's CPU, screen density and language.
// They are not installable one at a time. Android takes the whole set in a
// single install session or none of it, so a split that arrives on its own is
// rejected — which is why "unzip it and upload the base" produces an app that
// either refuses to install or crashes reaching for a library that is not there.
//
// The archives carrying them are ZIPs under a few names: .xapk from APKPure,
// .apks from bundletool and SAI. Some hold a manifest.json describing the set;
// plenty hold nothing but the APKs. Neither format is an Android standard, so
// this reads none of that paperwork — it takes the APKs and lets them speak for
// themselves, which works for both shapes and cannot be lied to by a manifest.

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

const (
	// maxArchiveParts bounds a split set. A real one is a base and a handful of
	// config splits; far beyond that is a mistake or a zip bomb.
	maxArchiveParts = 64
	// maxPartBytes bounds a single APK inside the archive, so a small download
	// cannot decompress into a disk-filling one.
	maxPartBytes = 512 << 20
	// maxArchiveBytes bounds the archive itself.
	maxArchiveBytes = 2 << 30
)

// UnpackArchive lays an archive's parts out under this store's root. The store
// keeps its own root private; callers have no reason to know it.
func (s *Store) UnpackArchive(r io.Reader) (*SplitSet, error) {
	return UnpackArchive(r, s.root)
}

// SplitSet is an unpacked archive: a base APK and its splits, on disk.
type SplitSet struct {
	// Dir holds the parts, and nothing else.
	Dir string
	// Base is the file name within Dir of the APK the others attach to.
	Base string
	// Parts is every part's file name, base first. The order is the order they
	// are written into the install session.
	Parts []string
}

// IsArchiveName reports whether a name looks like a split-APK archive rather
// than a plain APK.
func IsArchiveName(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".xapk", ".apks", ".apkm":
		return true
	}
	return false
}

// UnpackArchive reads a split-APK archive and lays its parts out in a new
// directory under root, returning what it found.
//
// The caller owns the directory afterwards, including removing it if a later
// step fails — the archive is on disk by then and this cannot know whether the
// upload as a whole succeeded.
func UnpackArchive(r io.Reader, root string) (*SplitSet, error) {
	tmp, err := os.CreateTemp(root, "archive-*.zip")
	if err != nil {
		return nil, err
	}
	defer func() {
		tmp.Close()
		os.Remove(tmp.Name())
	}()

	// One byte past the limit is enough to know it was exceeded.
	size, err := io.Copy(tmp, io.LimitReader(r, maxArchiveBytes+1))
	if err != nil {
		return nil, err
	}
	if size > maxArchiveBytes {
		return nil, fmt.Errorf("archive is larger than the %d MB limit", maxArchiveBytes>>20)
	}

	zr, err := zip.NewReader(tmp, size)
	if err != nil {
		return nil, fmt.Errorf("not a readable archive: %w", err)
	}

	apks, err := apkEntries(zr)
	if err != nil {
		return nil, err
	}

	dir, err := os.MkdirTemp(root, "unpacked-*")
	if err != nil {
		return nil, err
	}
	set := &SplitSet{Dir: dir}
	for _, f := range apks {
		// Flattened deliberately: the parts go into one session by name, the
		// folders inside the archive mean nothing to the installer, and a
		// flattened name cannot climb out of the directory.
		name := sanitize(path.Base(f.Name))
		if err := extractPart(f, filepath.Join(dir, name)); err != nil {
			os.RemoveAll(dir)
			return nil, err
		}
		set.Parts = append(set.Parts, name)
	}

	set.Base = pickBase(dir, set.Parts)
	if set.Base == "" {
		os.RemoveAll(dir)
		return nil, errors.New("archive has no base APK — every part is a split")
	}
	// Base first: it is the one whose manifest names the package, and the one
	// worth writing into the session before anything that attaches to it.
	sort.SliceStable(set.Parts, func(i, j int) bool { return set.Parts[i] == set.Base })
	return set, nil
}

// apkEntries picks the installable parts out of an archive and rejects the
// shapes that cannot be installed at all.
func apkEntries(zr *zip.Reader) ([]*zip.File, error) {
	var apks []*zip.File
	obb := false
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		name := f.Name
		// OBB expansion files are not installed — they are copied to external
		// storage under the app's own folder, which a silent installer has no
		// business doing. Better to say so than to install an app that starts
		// and then cannot find its data.
		if strings.Contains(strings.ToLower(name), "/obb/") || strings.HasSuffix(strings.ToLower(name), ".obb") {
			obb = true
			continue
		}
		if !strings.EqualFold(filepath.Ext(name), ".apk") {
			continue // manifest.json, icons, whatever else the packager added
		}
		if f.UncompressedSize64 > maxPartBytes {
			return nil, fmt.Errorf("%s is larger than the %d MB limit for one part",
				path.Base(name), maxPartBytes>>20)
		}
		apks = append(apks, f)
		if len(apks) > maxArchiveParts {
			return nil, fmt.Errorf("archive holds more than %d APKs", maxArchiveParts)
		}
	}
	if len(apks) == 0 {
		if obb {
			return nil, errors.New("archive holds OBB expansion data but no APK")
		}
		return nil, errors.New("archive holds no APK files")
	}
	if obb {
		return nil, errors.New("archive needs OBB expansion files, which cannot be installed silently")
	}
	return apks, nil
}

func extractPart(f *zip.File, dest string) error {
	rc, err := f.Open()
	if err != nil {
		return fmt.Errorf("cannot read %s: %w", path.Base(f.Name), err)
	}
	defer rc.Close()

	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()
	// Limited rather than trusted: UncompressedSize64 is a claim the archive
	// makes about itself, and the copy is what actually lands on the disk.
	n, err := io.Copy(out, io.LimitReader(rc, maxPartBytes+1))
	if err != nil {
		return err
	}
	if n > maxPartBytes {
		return fmt.Errorf("%s is larger than the %d MB limit for one part",
			path.Base(f.Name), maxPartBytes>>20)
	}
	return out.Close()
}

// pickBase names the APK the others attach to.
//
// "base.apk" is what bundletool and SAI produce and what APKPure keeps for the
// main APK often enough to try first. Failing that the base is the biggest part
// by a wide margin — a config split carries one ABI's libraries or one density's
// drawables, never the code — and a name marked "split" or "config" is never it.
func pickBase(dir string, parts []string) string {
	for _, p := range parts {
		if strings.EqualFold(p, "base.apk") {
			return p
		}
	}
	best, bestSize := "", int64(-1)
	for _, p := range parts {
		lower := strings.ToLower(p)
		if strings.HasPrefix(lower, "split_") || strings.HasPrefix(lower, "config.") ||
			strings.Contains(lower, ".config.") {
			continue
		}
		if st, err := os.Stat(filepath.Join(dir, p)); err == nil && st.Size() > bestSize {
			best, bestSize = p, st.Size()
		}
	}
	return best
}

// AdoptSplitSet moves an unpacked set to its final name under the store root,
// replacing anything already there. The name is the catalogue key, so this is
// what makes the upload visible.
func (s *Store) AdoptSplitSet(set *SplitSet, name string) (string, error) {
	final := filepath.Join(s.root, sanitize(name))
	absRoot, err := filepath.Abs(s.root)
	if err != nil {
		return "", err
	}
	absFinal, err := filepath.Abs(final)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(absFinal, absRoot+string(os.PathSeparator)) {
		return "", os.ErrPermission
	}
	// Re-uploading a package replaces it, the way uploading an APK over one
	// with the same name does.
	if err := os.RemoveAll(final); err != nil {
		return "", err
	}
	if err := os.Rename(set.Dir, final); err != nil {
		return "", err
	}
	set.Dir = final
	return final, nil
}

// OpenPart opens one part of a split package, rejecting anything that tries to
// reach outside it.
func (s *Store) OpenPart(pkgName, part string) (*os.File, error) {
	dir := sanitize(pkgName)
	base := sanitize(part)
	if !strings.HasSuffix(strings.ToLower(base), ".apk") {
		return nil, os.ErrPermission
	}
	full := filepath.Join(s.root, dir, base)
	absRoot, _ := filepath.Abs(filepath.Join(s.root, dir))
	absFull, _ := filepath.Abs(full)
	if !strings.HasPrefix(absFull, absRoot+string(os.PathSeparator)) {
		return nil, os.ErrPermission
	}
	return os.Open(full)
}

// PartsOf lists the parts of a split package, base first, or nil if the name is
// not a split package in this store.
func (s *Store) PartsOf(pkgName string) []string {
	dir := filepath.Join(s.root, sanitize(pkgName))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var parts []string
	for _, e := range entries {
		if !e.IsDir() && strings.EqualFold(filepath.Ext(e.Name()), ".apk") {
			parts = append(parts, e.Name())
		}
	}
	if len(parts) == 0 {
		return nil
	}
	sort.Strings(parts)
	base := pickBase(dir, parts)
	sort.SliceStable(parts, func(i, j int) bool { return parts[i] == base })
	return parts
}
