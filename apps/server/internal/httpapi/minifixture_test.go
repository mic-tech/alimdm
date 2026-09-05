package httpapi

// A minimal APK, built rather than checked in.
//
// The split tests need a base APK whose manifest names a chosen package, because
// the package name is what the stored name comes from. The alternative — reusing
// the repository's 60MB release APK — would put a quarter of a gigabyte of
// temporary zip through the suite for one string, and would pin the tests to
// whatever that build happens to be called.
//
// So this writes Android's binary XML directly: a string pool and one <manifest>
// element carrying package and versionName. It is exactly as much of the format
// as apkinfo reads, and no more.

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"testing"
	"time"
)

func nowRFC3339() string { return time.Now().UTC().Format(time.RFC3339) }

// miniAPK returns the bytes of a zip holding a binary AndroidManifest.xml that
// names pkg.
func miniAPK(t *testing.T, pkg string) []byte {
	t.Helper()
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	w, err := zw.Create("AndroidManifest.xml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(binaryManifest(pkg, "1.0")); err != nil {
		t.Fatal(err)
	}
	// A dex file, so the archive looks like an APK rather than a bare manifest.
	w, err = zw.Create("classes.dex")
	if err != nil {
		t.Fatal(err)
	}
	w.Write([]byte("dex"))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func binaryManifest(pkg, versionName string) []byte {
	pool := []string{"manifest", "package", "versionName", pkg, versionName}
	const (
		typeStringPool = 0x0001
		typeStartElem  = 0x0102
		flagUTF8       = 1 << 8
		attrTypeString = 0x03
	)

	// ── string pool ──
	offsets := make([]uint32, len(pool))
	var data bytes.Buffer
	for i, s := range pool {
		offsets[i] = uint32(data.Len())
		// UTF-8 entries are prefixed with a character count and a byte count,
		// each one byte for anything this short, and NUL-terminated.
		data.WriteByte(byte(len([]rune(s))))
		data.WriteByte(byte(len(s)))
		data.WriteString(s)
		data.WriteByte(0)
	}
	for data.Len()%4 != 0 {
		data.WriteByte(0)
	}
	stringsStart := uint32(28 + 4*len(pool))
	poolSize := stringsStart + uint32(data.Len())

	pc := make([]byte, 28)
	binary.LittleEndian.PutUint16(pc[0:], typeStringPool)
	binary.LittleEndian.PutUint16(pc[2:], 28)
	binary.LittleEndian.PutUint32(pc[4:], poolSize)
	binary.LittleEndian.PutUint32(pc[8:], uint32(len(pool)))
	binary.LittleEndian.PutUint32(pc[12:], 0) // no styles
	binary.LittleEndian.PutUint32(pc[16:], flagUTF8)
	binary.LittleEndian.PutUint32(pc[20:], stringsStart)
	binary.LittleEndian.PutUint32(pc[24:], 0)
	for _, off := range offsets {
		pc = binary.LittleEndian.AppendUint32(pc, off)
	}
	pc = append(pc, data.Bytes()...)

	// ── <manifest package="…" versionName="…"> ──
	// Header (8) + lineNumber, comment, ns, name (4 each) = 24, then the
	// attribute block descriptor, then the attributes themselves.
	const attrSize = 20
	attrs := []struct{ name, value uint32 }{
		{1, 3}, // package  = pkg
		{2, 4}, // versionName = versionName
	}
	elem := make([]byte, 36)
	binary.LittleEndian.PutUint16(elem[0:], typeStartElem)
	binary.LittleEndian.PutUint16(elem[2:], 16)
	binary.LittleEndian.PutUint32(elem[8:], 1)           // lineNumber
	binary.LittleEndian.PutUint32(elem[12:], 0xFFFFFFFF) // comment: none
	binary.LittleEndian.PutUint32(elem[16:], 0xFFFFFFFF) // namespace: none
	binary.LittleEndian.PutUint32(elem[20:], 0)          // name = "manifest"
	// attributeStart is measured from the start of the attribute extension,
	// which begins 16 bytes into the chunk — not from the chunk start.
	binary.LittleEndian.PutUint16(elem[24:], 20)
	binary.LittleEndian.PutUint16(elem[26:], attrSize)
	binary.LittleEndian.PutUint16(elem[28:], uint16(len(attrs)))

	for _, a := range attrs {
		e := make([]byte, attrSize)
		binary.LittleEndian.PutUint32(e[0:], 0xFFFFFFFF) // namespace
		binary.LittleEndian.PutUint32(e[4:], a.name)
		binary.LittleEndian.PutUint32(e[8:], a.value) // raw value
		binary.LittleEndian.PutUint16(e[12:], 8)      // typed value size
		e[14] = 0                                     // res0
		e[15] = attrTypeString
		binary.LittleEndian.PutUint32(e[16:], a.value)
		elem = append(elem, e...)
	}
	binary.LittleEndian.PutUint32(elem[4:], uint32(len(elem)))

	// ── file header, then the chunks ──
	total := 8 + len(pc) + len(elem)
	head := make([]byte, 8)
	binary.LittleEndian.PutUint16(head[0:], 0x0003) // XML
	binary.LittleEndian.PutUint16(head[2:], 8)
	binary.LittleEndian.PutUint32(head[4:], uint32(total))

	out := append(head, pc...)
	return append(out, elem...)
}
