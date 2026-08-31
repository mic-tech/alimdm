package apkinfo

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

// Real APKs are the only honest test of a binary-format parser, so these run
// against the built artefacts when they are present and skip when they are not
// (a fresh clone has no APKs).
func TestReadsRealAPK(t *testing.T) {
	for _, tc := range []struct {
		path        string
		wantPackage string
	}{
		{"../../../../apk/alimdm-release.apk", "com.alimdm"},
	} {
		p, err := filepath.Abs(tc.path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(p); err != nil {
			t.Skipf("no APK at %s", p)
		}
		info, err := ReadAPK(p)
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		if info.PackageName != tc.wantPackage {
			t.Errorf("package: got %q want %q", info.PackageName, tc.wantPackage)
		}
		if info.VersionCode <= 0 {
			t.Errorf("versionCode should be positive, got %d", info.VersionCode)
		}
		if info.VersionName == "" {
			t.Error("versionName should not be empty")
		}
		t.Logf("%s -> %s %d/%s", filepath.Base(p), info.PackageName, info.VersionCode, info.VersionName)
	}
}

// A third-party APK exercises a manifest this project did not produce.
func TestReadsThirdPartyAPK(t *testing.T) {
	matches, _ := filepath.Glob("../../../../apk/com.*.apk")
	if len(matches) == 0 {
		t.Skip("no third-party APKs available")
	}
	for _, m := range matches {
		info, err := ReadAPK(m)
		if err != nil {
			t.Errorf("%s: %v", filepath.Base(m), err)
			continue
		}
		if info.PackageName == "" || info.VersionCode <= 0 {
			t.Errorf("%s: incomplete info %+v", filepath.Base(m), info)
		}
		t.Logf("%s -> %s %d/%s", filepath.Base(m), info.PackageName, info.VersionCode, info.VersionName)
	}
}

// Uploaded files are untrusted: garbage must produce an error, never a panic.
func TestRejectsMalformedInput(t *testing.T) {
	dir := t.TempDir()

	notZip := filepath.Join(dir, "not.apk")
	if err := os.WriteFile(notZip, []byte("this is not a zip"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadAPK(notZip); err == nil {
		t.Error("a non-zip should be rejected")
	}

	// A valid zip with no manifest.
	noManifest := filepath.Join(dir, "empty.apk")
	f, err := os.Create(noManifest)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, _ := zw.Create("classes.dex")
	_, _ = w.Write([]byte("dex"))
	zw.Close()
	f.Close()
	if _, err := ReadAPK(noManifest); err == nil {
		t.Error("a zip without a manifest should be rejected")
	}

	// A manifest of random bytes must error rather than crash.
	junk := filepath.Join(dir, "junk.apk")
	f2, err := os.Create(junk)
	if err != nil {
		t.Fatal(err)
	}
	zw2 := zip.NewWriter(f2)
	w2, _ := zw2.Create("AndroidManifest.xml")
	_, _ = w2.Write([]byte{0x03, 0x00, 0x08, 0x00, 0xff, 0xff, 0xff, 0x7f, 0x41, 0x42, 0x43})
	zw2.Close()
	f2.Close()
	if _, err := ReadAPK(junk); err == nil {
		t.Error("a corrupt manifest should be rejected")
	}
}
