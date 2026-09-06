package apk

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// zipOf builds an archive in memory from name → contents.
func zipOf(t *testing.T, files map[string]string) *bytes.Reader {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return bytes.NewReader(buf.Bytes())
}

// The shape the operator actually had: a bare ZIP of a base and its splits,
// with no manifest.json to describe them.
func TestUnpackArchiveWithoutAManifest(t *testing.T) {
	root := t.TempDir()
	set, err := UnpackArchive(zipOf(t, map[string]string{
		"base.apk":    "the base",
		"split_0.apk": "arm64",
		"split_1.apk": "xxhdpi",
		"split_2.apk": "en",
	}), root)
	if err != nil {
		t.Fatal(err)
	}
	if set.Base != "base.apk" {
		t.Fatalf("base = %q, want base.apk", set.Base)
	}
	if len(set.Parts) != 4 {
		t.Fatalf("%d parts, want 4", len(set.Parts))
	}
	if set.Parts[0] != "base.apk" {
		t.Fatalf("parts[0] = %q — the base must be written into the session first", set.Parts[0])
	}
	for _, p := range set.Parts {
		if _, err := os.Stat(filepath.Join(set.Dir, p)); err != nil {
			t.Fatalf("%s was not extracted: %v", p, err)
		}
	}
}

// APKPure's shape: a manifest.json, an icon, and the APKs named after the
// package rather than "base".
func TestUnpackArchiveIgnoresThePackagersPaperwork(t *testing.T) {
	root := t.TempDir()
	set, err := UnpackArchive(zipOf(t, map[string]string{
		"manifest.json":                   `{"package_name":"com.example.app"}`,
		"icon.png":                        "not an apk",
		"com.example.app.apk":             strings.Repeat("base", 200),
		"config.arm64_v8a.apk":            "libs",
		"config.en.apk":                   "strings",
		"unknown/nested/config.xhdpi.apk": "drawables",
	}), root)
	if err != nil {
		t.Fatal(err)
	}
	if set.Base != "com.example.app.apk" {
		t.Fatalf("base = %q, want the APK that is not a config split", set.Base)
	}
	if len(set.Parts) != 4 {
		t.Fatalf("%d parts, want the 4 APKs and neither the manifest nor the icon", len(set.Parts))
	}
	for _, p := range set.Parts {
		if strings.Contains(p, "/") {
			t.Fatalf("part %q kept a folder from the archive", p)
		}
	}
}

// An archive is operator input. A part named to climb out of the directory must
// land inside it like any other.
func TestUnpackArchiveCannotEscapeItsDirectory(t *testing.T) {
	root := t.TempDir()
	set, err := UnpackArchive(zipOf(t, map[string]string{
		"base.apk":            "the base",
		"../../../evil.apk":   "escape",
		"/absolute/other.apk": "escape",
	}), root)
	if err != nil {
		t.Fatal(err)
	}
	absDir, _ := filepath.Abs(set.Dir)
	for _, p := range set.Parts {
		abs, _ := filepath.Abs(filepath.Join(set.Dir, p))
		if filepath.Dir(abs) != absDir {
			t.Fatalf("part %q landed at %q, outside %q", p, abs, absDir)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "evil.apk")); err == nil {
		t.Fatal("a part escaped into the store root")
	}
}

// OBB expansion data is copied to external storage, not installed. Saying so is
// better than installing an app that starts and cannot find its data.
func TestUnpackArchiveRefusesOBBData(t *testing.T) {
	root := t.TempDir()
	_, err := UnpackArchive(zipOf(t, map[string]string{
		"base.apk": "the base",
		"Android/obb/com.example.app/main.1.com.example.app.obb": "big data",
	}), root)
	if err == nil {
		t.Fatal("accepted an archive whose data cannot be installed")
	}
	if !strings.Contains(err.Error(), "OBB") {
		t.Fatalf("error %q does not say what is wrong", err)
	}
}

func TestUnpackArchiveRefusesAnArchiveWithNoAPKs(t *testing.T) {
	root := t.TempDir()
	if _, err := UnpackArchive(zipOf(t, map[string]string{
		"manifest.json": "{}", "readme.txt": "hello",
	}), root); err == nil {
		t.Fatal("accepted an archive with nothing to install")
	}
}

func TestUnpackArchiveRefusesSomethingThatIsNotAZip(t *testing.T) {
	root := t.TempDir()
	if _, err := UnpackArchive(strings.NewReader("this is not a zip file"), root); err == nil {
		t.Fatal("accepted a file that is not an archive")
	}
}

// A failed unpack must not leave parts lying in the store root, where List
// would later offer them as installable packages.
func TestAFailedUnpackLeavesNothingBehind(t *testing.T) {
	root := t.TempDir()
	_, _ = UnpackArchive(zipOf(t, map[string]string{
		"base.apk":                             "the base",
		"Android/obb/com.example.app/main.obb": "data",
	}), root)
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		names := []string{}
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("store root still holds %v", names)
	}
}

// Once adopted, a split package is one entry in the catalogue, its parts are
// readable, and deleting it takes the whole set.
func TestAdoptedSplitPackageBehavesLikeOneThing(t *testing.T) {
	root := t.TempDir()
	st := NewStore(root)
	set, err := st.UnpackArchive(zipOf(t, map[string]string{
		"base.apk": "the base", "split_0.apk": "arm64",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AdoptSplitSet(set, "com.example.app.xapk"); err != nil {
		t.Fatal(err)
	}

	names, err := st.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0] != "com.example.app.xapk" {
		t.Fatalf("List = %v, want one split package", names)
	}

	parts := st.PartsOf("com.example.app.xapk")
	if len(parts) != 2 || parts[0] != "base.apk" {
		t.Fatalf("PartsOf = %v, want base first", parts)
	}
	f, err := st.OpenPart("com.example.app.xapk", "split_0.apk")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	// A part name cannot be used to read something else.
	if _, err := st.OpenPart("com.example.app.xapk", "../../etc/passwd"); err == nil {
		t.Fatal("a traversing part name was opened")
	}

	if err := st.Delete("com.example.app.xapk"); err != nil {
		t.Fatal(err)
	}
	if names, _ := st.List(); len(names) != 0 {
		t.Fatalf("List = %v after delete, want empty", names)
	}
}

// Re-uploading replaces, the way uploading an APK over one of the same name does.
func TestAdoptingTwiceReplacesTheOlderSet(t *testing.T) {
	root := t.TempDir()
	st := NewStore(root)
	for _, body := range []string{"first", "second"} {
		set, err := st.UnpackArchive(zipOf(t, map[string]string{
			"base.apk": body, "split_0.apk": body,
		}))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.AdoptSplitSet(set, "com.example.app.xapk"); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(filepath.Join(root, "com.example.app.xapk", "base.apk"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "second" {
		t.Fatalf("base.apk holds %q, want the newer upload", got)
	}
	if names, _ := st.List(); len(names) != 1 {
		t.Fatalf("List = %v, want one entry after re-upload", names)
	}
}

// A plain APK must keep behaving exactly as it did.
func TestPlainAPKsAreUnaffected(t *testing.T) {
	root := t.TempDir()
	st := NewStore(root)
	body := strings.Repeat("x", 60*1024) // over the 50KB sanity floor
	if _, _, _, err := st.Ingest(strings.NewReader(body), "worksheet.apk"); err != nil {
		t.Fatal(err)
	}
	names, _ := st.List()
	if len(names) != 1 || names[0] != "worksheet.apk" {
		t.Fatalf("List = %v", names)
	}
	if st.PartsOf("worksheet.apk") != nil {
		t.Fatal("a plain APK was reported as a split package")
	}
	if err := st.Delete("worksheet.apk"); err != nil {
		t.Fatal(err)
	}
}

func TestIsArchiveName(t *testing.T) {
	for _, yes := range []string{"app.xapk", "App.XAPK", "bundle.apks", "thing.apkm"} {
		if !IsArchiveName(yes) {
			t.Errorf("IsArchiveName(%q) = false", yes)
		}
	}
	for _, no := range []string{"app.apk", "app.zip", "app", "app.apk.txt"} {
		if IsArchiveName(no) {
			t.Errorf("IsArchiveName(%q) = true", no)
		}
	}
}

// The exact shape mic-tech/app-packager writes: a base, splits named after
// their own split id, an icon, and an XAPK v2 manifest.json. Its APK entries
// are stored uncompressed, which is a different zip method from the deflated
// archives elsewhere in these tests.
func TestUnpacksAppPackagerOutput(t *testing.T) {
	root := t.TempDir()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	store := func(name, body string) {
		t.Helper()
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	store("base.apk", "the base")
	store("config.arm64_v8a.apk", "native libraries")
	store("config.en.apk", "english strings")
	store("config.xxhdpi.apk", "drawables")
	deflated, _ := zw.Create("icon.png")
	deflated.Write([]byte("PNG"))
	deflated, _ = zw.Create("manifest.json")
	deflated.Write([]byte(`{"package_name":"com.example.app","split_apks":[{"id":"base","file":"base.apk"}]}`))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	set, err := UnpackArchive(bytes.NewReader(buf.Bytes()), root)
	if err != nil {
		t.Fatal(err)
	}
	if set.Base != "base.apk" {
		t.Fatalf("base = %q, want base.apk", set.Base)
	}
	if len(set.Parts) != 4 {
		t.Fatalf("parts = %v, want the four APKs and neither the icon nor the manifest", set.Parts)
	}
	if set.Parts[0] != "base.apk" {
		t.Fatalf("parts[0] = %q, want the base first", set.Parts[0])
	}
	got, err := os.ReadFile(filepath.Join(set.Dir, "config.en.apk"))
	if err != nil || string(got) != "english strings" {
		t.Fatalf("uncompressed entry did not survive extraction: %q, %v", got, err)
	}
}

// The same tool, with OBB expansion files included. Refused, and the message
// has to say why — the APKs alone would install an app that starts and cannot
// find its data, which is worse than not installing it.
func TestRefusesAppPackagerOutputWithOBB(t *testing.T) {
	root := t.TempDir()
	_, err := UnpackArchive(zipOf(t, map[string]string{
		"base.apk":             "the base",
		"config.arm64_v8a.apk": "native libraries",
		"icon.png":             "PNG",
		"manifest.json":        `{"package_name":"com.example.app","expansions":[{"file":"main.1.obb"}]}`,
		"Android/obb/com.example.app/main.1.com.example.app.obb": "the app's data",
	}), root)
	if err == nil {
		t.Fatal("accepted an archive whose expansion data cannot be installed")
	}
	if !strings.Contains(err.Error(), "OBB") {
		t.Fatalf("error %q does not name the problem", err)
	}
}
