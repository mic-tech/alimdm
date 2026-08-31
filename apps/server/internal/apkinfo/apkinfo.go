// Package apkinfo reads an APK's package name and version straight out of the
// file, so an operator uploading a build does not have to type numbers that
// have to match it exactly.
//
// AndroidManifest.xml inside an APK is not text: it is Android's binary XML
// (AXML), a chunked format with a string pool. There is no standard library
// support for it and pulling in a dependency for four fields is not worth it,
// so this reads the small part that matters — the attributes of the single
// <manifest> element — and ignores everything else.
//
// Input here is an uploaded file, so every read is bounds-checked and a
// malformed APK returns an error rather than panicking.
package apkinfo

import (
	"archive/zip"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"unicode/utf16"
)

// Info is what an upload needs to know about a build.
type Info struct {
	PackageName string
	VersionCode int
	VersionName string
}

const (
	chunkStringPool  = 0x0001
	chunkStartElem   = 0x0102
	stringPoolUTF8   = 1 << 8
	attrTypeString   = 0x03
	attrTypeIntDec   = 0x10
	attrTypeIntHex   = 0x11
	manifestFileName = "AndroidManifest.xml"
	// A manifest far larger than this is not something we should be parsing.
	maxManifestBytes = 8 << 20
)

// ReadAPK extracts the package name and version from an APK on disk.
func ReadAPK(path string) (Info, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return Info{}, fmt.Errorf("not a readable APK: %w", err)
	}
	defer zr.Close()

	for _, f := range zr.File {
		if f.Name != manifestFileName {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return Info{}, fmt.Errorf("cannot read %s: %w", manifestFileName, err)
		}
		defer rc.Close()
		raw, err := io.ReadAll(io.LimitReader(rc, maxManifestBytes))
		if err != nil {
			return Info{}, fmt.Errorf("cannot read %s: %w", manifestFileName, err)
		}
		return parseManifest(raw)
	}
	return Info{}, errors.New("APK contains no AndroidManifest.xml")
}

func parseManifest(b []byte) (Info, error) {
	if len(b) < 8 {
		return Info{}, errors.New("manifest too short")
	}
	// File header is a chunk header; skip it and walk the top-level chunks.
	pos := 8
	var pool []string
	for pos+8 <= len(b) {
		typ := binary.LittleEndian.Uint16(b[pos:])
		size := binary.LittleEndian.Uint32(b[pos+4:])
		if size < 8 || int(size) > len(b)-pos {
			return Info{}, errors.New("malformed manifest chunk")
		}
		chunk := b[pos : pos+int(size)]

		switch typ {
		case chunkStringPool:
			p, err := parseStringPool(chunk)
			if err != nil {
				return Info{}, err
			}
			pool = p
		case chunkStartElem:
			info, done, err := parseStartElement(chunk, pool)
			if err != nil {
				return Info{}, err
			}
			if done {
				return info, nil
			}
		}
		pos += int(size)
	}
	return Info{}, errors.New("manifest has no <manifest> element")
}

func parseStringPool(c []byte) ([]string, error) {
	if len(c) < 28 {
		return nil, errors.New("string pool too short")
	}
	count := binary.LittleEndian.Uint32(c[8:])
	flags := binary.LittleEndian.Uint32(c[16:])
	stringsStart := binary.LittleEndian.Uint32(c[20:])
	utf8 := flags&stringPoolUTF8 != 0

	if int(count) > (len(c)-28)/4 {
		return nil, errors.New("string pool count out of range")
	}
	out := make([]string, count)
	for i := 0; i < int(count); i++ {
		off := binary.LittleEndian.Uint32(c[28+4*i:])
		at := int(stringsStart) + int(off)
		if at < 0 || at >= len(c) {
			return nil, errors.New("string offset out of range")
		}
		s, err := readPoolString(c[at:], utf8)
		if err != nil {
			return nil, err
		}
		out[i] = s
	}
	return out, nil
}

// readPoolString decodes one entry. Both encodings prefix a length that uses a
// high bit to signal a two-unit form for long strings.
func readPoolString(b []byte, utf8 bool) (string, error) {
	if utf8 {
		if len(b) < 2 {
			return "", errors.New("truncated utf8 string")
		}
		i := 0
		if b[i]&0x80 != 0 { // character count, possibly two bytes
			i += 2
		} else {
			i++
		}
		if i >= len(b) {
			return "", errors.New("truncated utf8 string")
		}
		n := int(b[i])
		if b[i]&0x80 != 0 {
			if i+1 >= len(b) {
				return "", errors.New("truncated utf8 string")
			}
			n = (int(b[i]&0x7F) << 8) | int(b[i+1])
			i += 2
		} else {
			i++
		}
		if i+n > len(b) {
			return "", errors.New("utf8 string runs past the pool")
		}
		return string(b[i : i+n]), nil
	}

	if len(b) < 2 {
		return "", errors.New("truncated utf16 string")
	}
	n := int(binary.LittleEndian.Uint16(b))
	i := 2
	if n&0x8000 != 0 {
		if len(b) < 4 {
			return "", errors.New("truncated utf16 string")
		}
		n = ((n & 0x7FFF) << 16) | int(binary.LittleEndian.Uint16(b[2:]))
		i = 4
	}
	if i+n*2 > len(b) {
		return "", errors.New("utf16 string runs past the pool")
	}
	u := make([]uint16, n)
	for k := 0; k < n; k++ {
		u[k] = binary.LittleEndian.Uint16(b[i+2*k:])
	}
	return string(utf16.Decode(u)), nil
}

// parseStartElement returns the Info when the element is <manifest>; the second
// result reports whether that was it, so the caller can stop walking.
func parseStartElement(c []byte, pool []string) (Info, bool, error) {
	// Chunk header (8) + lineNumber, comment, ns, name (4 each) = 24, then the
	// attribute block descriptors.
	if len(c) < 36 {
		return Info{}, false, nil
	}
	nameIdx := binary.LittleEndian.Uint32(c[20:])
	if int(nameIdx) >= len(pool) || pool[nameIdx] != "manifest" {
		return Info{}, false, nil
	}

	// attributeStart is relative to ResXMLTree_attrExt, which begins after the
	// 8-byte chunk header and the 8 bytes of lineNumber+comment — not at the
	// chunk start. Getting this wrong finds the element but no attributes.
	const attrExtBase = 16
	attrStart := int(binary.LittleEndian.Uint16(c[24:]))
	attrSize := int(binary.LittleEndian.Uint16(c[26:]))
	attrCount := int(binary.LittleEndian.Uint16(c[28:]))
	if attrSize < 20 {
		attrSize = 20
	}

	var info Info
	for i := 0; i < attrCount; i++ {
		at := attrExtBase + attrStart + i*attrSize
		if at+20 > len(c) {
			return Info{}, false, errors.New("attribute runs past the element")
		}
		nameRef := binary.LittleEndian.Uint32(c[at+4:])
		dataType := c[at+15]
		data := binary.LittleEndian.Uint32(c[at+16:])
		if int(nameRef) >= len(pool) {
			continue
		}
		switch pool[nameRef] {
		case "package":
			info.PackageName = attrString(c, at, pool, dataType, data)
		case "versionCode":
			if dataType == attrTypeIntDec || dataType == attrTypeIntHex {
				info.VersionCode = int(data)
			}
		case "versionName":
			info.VersionName = attrString(c, at, pool, dataType, data)
		}
	}
	if info.PackageName == "" {
		return Info{}, false, errors.New("manifest has no package name")
	}
	return info, true, nil
}

// attrString resolves a string-typed attribute, falling back to the raw value
// index that aapt also writes.
func attrString(c []byte, at int, pool []string, dataType byte, data uint32) string {
	if dataType == attrTypeString && int(data) < len(pool) {
		return pool[data]
	}
	raw := binary.LittleEndian.Uint32(c[at+8:])
	if int(raw) < len(pool) {
		return pool[raw]
	}
	return ""
}
