package imaging

import (
	"bytes"
	"encoding/binary"
	"errors"
)

// errMalformedJPEG is a JPEG whose marker structure cannot be followed. The
// decoder may still read it; only keeping its bytes as they are is given up.
var errMalformedJPEG = errors.New("malformed jpeg")

// The JPEG markers this file reads.
const (
	markerTEM   = 0x01
	markerRST0  = 0xd0
	markerRST7  = 0xd7
	markerSOI   = 0xd8
	markerEOI   = 0xd9
	markerSOS   = 0xda
	markerAPP0  = 0xe0
	markerAPP1  = 0xe1
	markerAPP2  = 0xe2
	markerAPP14 = 0xee
	markerAPP15 = 0xef
	markerCOM   = 0xfe
)

// orientationTag is the EXIF tag that says how a camera was held.
const orientationTag = 0x0112

// segment is one marker segment of a JPEG's header.
type segment struct {
	marker byte
	// data is the whole segment: 0xff, the marker, and for a marker that has
	// one, the two-byte length and the payload.
	data []byte
}

// payload returns what follows the segment's length.
func (s segment) payload() []byte {
	if len(s.data) < 4 {
		return nil
	}
	return s.data[4:]
}

// standalone reports whether a marker has no length and no payload.
func standalone(marker byte) bool {
	return marker == markerTEM || (marker >= markerRST0 && marker <= markerRST7)
}

// nextMarker reads the marker at data[i], skipping the 0xff fill bytes that
// may pad one. It returns where the marker's 0xff is, the marker, and where
// its length or payload begins.
func nextMarker(data []byte, i int) (int, byte, int, error) {
	if i >= len(data) || data[i] != 0xff {
		return 0, 0, 0, errMalformedJPEG
	}
	for i < len(data) && data[i] == 0xff {
		i++
	}
	if i >= len(data) {
		return 0, 0, 0, errMalformedJPEG
	}
	return i - 1, data[i], i + 1, nil
}

// segmentEnd returns where a segment whose length begins at data[i] ends.
func segmentEnd(data []byte, i int) (int, error) {
	if i+2 > len(data) {
		return 0, errMalformedJPEG
	}
	n := int(binary.BigEndian.Uint16(data[i:]))
	if n < 2 || i+n > len(data) {
		return 0, errMalformedJPEG
	}
	return i + n, nil
}

// jpegHeader returns the segments of a JPEG before its first scan and the
// offset of the start-of-scan marker that begins it.
func jpegHeader(data []byte) ([]segment, int, error) {
	if len(data) < 4 || data[0] != 0xff || data[1] != markerSOI {
		return nil, 0, errMalformedJPEG
	}
	var out []segment
	for i := 2; ; {
		start, marker, next, err := nextMarker(data, i)
		if err != nil {
			return nil, 0, err
		}
		if standalone(marker) {
			out = append(out, segment{marker: marker, data: data[start:next]})
			i = next
			continue
		}
		if marker == markerSOI || marker == markerEOI {
			return nil, 0, errMalformedJPEG
		}
		end, err := segmentEnd(data, next)
		if err != nil {
			return nil, 0, err
		}
		if marker == markerSOS {
			return out, start, nil
		}
		out = append(out, segment{marker: marker, data: data[start:end]})
		i = end
	}
}

// jpegEnd returns the offset just past the end-of-image marker of the image
// whose first scan begins at scan. Phones append more after it: a depth map,
// a second picture, the video of a motion photo.
func jpegEnd(data []byte, scan int) (int, error) {
	for i := scan; ; {
		_, marker, next, err := nextMarker(data, i)
		if err != nil {
			return 0, err
		}
		if marker == markerEOI {
			return next, nil
		}
		if standalone(marker) {
			i = next
			continue
		}
		if i, err = segmentEnd(data, next); err != nil {
			return 0, err
		}
		if marker == markerSOS {
			i = skipEntropy(data, i)
		}
	}
}

// skipEntropy returns the offset of the first marker at or after i in the
// entropy-coded data of a scan. Inside it a 0xff is followed by a stuffed
// zero, a restart marker, or more 0xff fill; anything else ends the scan.
func skipEntropy(data []byte, i int) int {
	for i+1 < len(data) {
		if data[i] != 0xff {
			i++
			continue
		}
		switch m := data[i+1]; {
		case m == 0x00, m >= markerRST0 && m <= markerRST7:
			i += 2
		case m == 0xff:
			i++
		default:
			return i
		}
	}
	return len(data)
}

// stripJPEG returns a JPEG's bytes without its metadata and without anything
// appended after the image. It keeps what decoding depends on: the JFIF and
// Adobe segments, which say how the colours are coded, and the ICC profile.
// The EXIF segment goes, and with it where the photo was taken.
func stripJPEG(data []byte) ([]byte, error) {
	segments, scan, err := jpegHeader(data)
	if err != nil {
		return nil, err
	}
	end, err := jpegEnd(data, scan)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, end)
	out = append(out, 0xff, markerSOI)
	for _, s := range segments {
		if keepSegment(s) {
			out = append(out, s.data...)
		}
	}
	return append(out, data[scan:end]...), nil
}

// keepSegment reports whether a header segment is kept by stripJPEG.
func keepSegment(s segment) bool {
	switch {
	case s.marker == markerCOM:
		return false
	case s.marker == markerAPP0, s.marker == markerAPP14:
		return true
	case s.marker == markerAPP2:
		return bytes.HasPrefix(s.payload(), []byte("ICC_PROFILE\x00"))
	case s.marker >= markerAPP0 && s.marker <= markerAPP15:
		return false
	}
	return true
}

// jpegOrientation returns the EXIF orientation of a JPEG, 1 (upright) when
// it has none or it cannot be read.
func jpegOrientation(data []byte) int {
	segments, _, err := jpegHeader(data)
	if err != nil {
		return 1
	}
	for _, s := range segments {
		if s.marker == markerAPP1 && bytes.HasPrefix(s.payload(), []byte("Exif\x00\x00")) {
			return exifOrientation(s.payload()[6:])
		}
	}
	return 1
}

// exifOrientation reads the orientation tag from the first image file
// directory of an EXIF TIFF structure, 1 when it is absent or unreadable.
func exifOrientation(tiff []byte) int {
	if len(tiff) < 8 {
		return 1
	}
	var order binary.ByteOrder
	switch string(tiff[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 1
	}
	if order.Uint16(tiff[2:]) != 42 {
		return 1
	}
	ifd := uint64(order.Uint32(tiff[4:]))
	if ifd < 8 || ifd+2 > uint64(len(tiff)) {
		return 1
	}
	count := uint64(order.Uint16(tiff[ifd:]))
	for k := range count {
		entry := ifd + 2 + 12*k
		if entry+12 > uint64(len(tiff)) {
			return 1
		}
		if order.Uint16(tiff[entry:]) != orientationTag {
			continue
		}
		// The value is a SHORT, stored in the first two bytes of the field.
		if order.Uint16(tiff[entry+2:]) != 3 {
			return 1
		}
		if v := int(order.Uint16(tiff[entry+8:])); v >= 1 && v <= 8 {
			return v
		}
		return 1
	}
	return 1
}
