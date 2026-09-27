package imaging_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"math/rand/v2"
	"os"
	"testing"

	"github.com/erlidev/eika/internal/imaging"
	"github.com/erlidev/eika/internal/provider"
)

func TestFitKeepsTheAspectRatioWithinThe1080pBox(t *testing.T) {
	cases := []struct {
		name         string
		w, h         int
		wantW, wantH int
	}{
		{"small stays", 800, 600, 800, 600},
		{"exactly 1080p stays", 1920, 1080, 1920, 1080},
		{"exactly portrait 1080p stays", 1080, 1920, 1080, 1920},
		{"4k landscape halves", 3840, 2160, 1920, 1080},
		{"4:3 photo is bound by its short side", 4032, 3024, 1440, 1080},
		{"portrait photo turns the box", 3024, 4032, 1080, 1440},
		{"wide banner is bound by its long side", 4000, 500, 1920, 240},
		{"tall page is bound by its long side", 1080, 5000, 415, 1920},
		{"a line never becomes nothing", 10000, 1, 1920, 1},
		{"one pixel stays", 1, 1, 1, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w, h := imaging.Fit(c.w, c.h)
			if w != c.wantW || h != c.wantH {
				t.Errorf("Fit(%d, %d) = %d×%d, want %d×%d", c.w, c.h, w, h, c.wantW, c.wantH)
			}
		})
	}
}

func TestPrepareKeepsASmallPNGLossless(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 64, 48))
	for y := range 48 {
		for x := range 64 {
			src.Set(x, y, color.NRGBA{R: uint8(x * 4), G: uint8(y * 5), B: uint8(x ^ y), A: 255})
		}
	}
	got := prepare(t, encodePNG(t, src))
	if got.MediaType != imaging.MediaPNG || got.Width != 64 || got.Height != 48 {
		t.Fatalf("got %s %d×%d, want image/png 64×48", got.MediaType, got.Width, got.Height)
	}
	out := decode(t, got)
	for y := range 48 {
		for x := range 64 {
			if !sameColor(out.At(x, y), src.At(x, y)) {
				t.Fatalf("pixel %d,%d = %v, want %v", x, y, out.At(x, y), src.At(x, y))
			}
		}
	}
}

func TestPrepareDownscalesALargeScreenshotToAPNG(t *testing.T) {
	got := prepare(t, encodePNG(t, solid(3840, 2160, color.RGBA{R: 30, G: 60, B: 90, A: 255})))
	if got.MediaType != imaging.MediaPNG || got.Width != 1920 || got.Height != 1080 {
		t.Fatalf("got %s %d×%d, want image/png 1920×1080", got.MediaType, got.Width, got.Height)
	}
	out := decode(t, got)
	if b := out.Bounds(); b.Dx() != 1920 || b.Dy() != 1080 {
		t.Fatalf("decoded %v, want 1920×1080", b)
	}
	if !near(out.At(960, 540), color.RGBA{R: 30, G: 60, B: 90, A: 255}, 1) {
		t.Errorf("centre = %v, want the source colour", out.At(960, 540))
	}
}

// A one-pixel checkerboard is the worst case for a resampler: one that picks
// pixels rather than averaging them turns it into moiré or solid black and
// white. Shrunk properly, it is an even grey.
func TestPrepareAveragesFineDetailWhenShrinking(t *testing.T) {
	src := image.NewGray(image.Rect(0, 0, 3001, 1999))
	for y := range 1999 {
		for x := range 3001 {
			if (x+y)%2 == 0 {
				src.SetGray(x, y, color.Gray{Y: 255})
			}
		}
	}
	got := prepare(t, encodePNG(t, src))
	out := decode(t, got)
	b := out.Bounds()
	for y := b.Min.Y + 2; y < b.Max.Y-2; y += 7 {
		for x := b.Min.X + 2; x < b.Max.X-2; x += 7 {
			r, _, _, _ := out.At(x, y).RGBA()
			if v := r >> 8; v < 100 || v > 155 {
				t.Fatalf("pixel %d,%d = %d, want an even grey near 128", x, y, v)
			}
		}
	}
}

func TestPrepareKeepsASmallJPEGsBytesWithoutItsMetadata(t *testing.T) {
	plain := encodeJPEG(t, quadrants(64, 32), 95)
	withExif := withAPP1(plain, exifOrientation(1))
	withTrailer := append(append([]byte(nil), withExif...), []byte("motion photo video data")...)
	got := prepare(t, withTrailer)
	if got.MediaType != imaging.MediaJPEG || got.Width != 64 || got.Height != 32 {
		t.Fatalf("got %s %d×%d, want image/jpeg 64×32", got.MediaType, got.Width, got.Height)
	}
	if bytes.Contains(got.Data, []byte("Exif")) {
		t.Error("the EXIF segment was kept")
	}
	if !bytes.HasSuffix(got.Data, []byte{0xff, 0xd9}) {
		t.Error("the data after the end of the image was kept")
	}
	// Nothing was compressed again: the scan data is the original's.
	if !bytes.Equal(got.Data, plain) {
		t.Errorf("the kept JPEG is %d bytes, want the original's %d without its metadata", len(got.Data), len(plain))
	}
}

func TestPrepareDownscalesALargePhotoToAJPEG(t *testing.T) {
	got := prepare(t, encodeJPEG(t, solid(4000, 3000, color.RGBA{R: 200, G: 100, B: 50, A: 255}), 90))
	if got.MediaType != imaging.MediaJPEG || got.Width != 1440 || got.Height != 1080 {
		t.Fatalf("got %s %d×%d, want image/jpeg 1440×1080", got.MediaType, got.Width, got.Height)
	}
	if !near(decode(t, got).At(700, 500), color.RGBA{R: 200, G: 100, B: 50, A: 255}, 8) {
		t.Error("the colour did not survive")
	}
}

// Every EXIF orientation is a quarter turn, a mirror, or both of the stored
// picture. The source has four coloured quadrants, so where each lands
// shows which way the picture was turned.
func TestPrepareTurnsAPhotoUprightByItsEXIFOrientation(t *testing.T) {
	// Stored: red top-left, green top-right, blue bottom-left, white
	// bottom-right. want is what is top-left, top-right, bottom-left, and
	// bottom-right once upright.
	red, green, blue, white := color.RGBA{R: 255, A: 255}, color.RGBA{G: 255, A: 255},
		color.RGBA{B: 255, A: 255}, color.RGBA{R: 255, G: 255, B: 255, A: 255}
	cases := []struct {
		orientation int
		transposed  bool
		want        [4]color.RGBA
	}{
		{1, false, [4]color.RGBA{red, green, blue, white}},
		{2, false, [4]color.RGBA{green, red, white, blue}},
		{3, false, [4]color.RGBA{white, blue, green, red}},
		{4, false, [4]color.RGBA{blue, white, red, green}},
		{5, true, [4]color.RGBA{red, blue, green, white}},
		{6, true, [4]color.RGBA{blue, red, white, green}},
		{7, true, [4]color.RGBA{white, green, blue, red}},
		{8, true, [4]color.RGBA{green, white, red, blue}},
	}
	for _, c := range cases {
		t.Run(string(rune('0'+c.orientation)), func(t *testing.T) {
			// Large enough to be scaled as well, so both steps are covered.
			const w, h = 2400, 1200
			got := prepare(t, withAPP1(encodeJPEG(t, quadrants(w, h), 95), exifOrientation(uint16(c.orientation))))
			wantW, wantH := imaging.Fit(w, h)
			if c.transposed {
				wantW, wantH = imaging.Fit(h, w)
			}
			if got.Width != wantW || got.Height != wantH {
				t.Fatalf("got %d×%d, want %d×%d", got.Width, got.Height, wantW, wantH)
			}
			out := decode(t, got)
			qx, qy := got.Width/4, got.Height/4
			at := [4]color.Color{out.At(qx, qy), out.At(3*qx, qy), out.At(qx, 3*qy), out.At(3*qx, 3*qy)}
			for i := range at {
				if !near(at[i], c.want[i], 24) {
					t.Errorf("quadrant %d = %v, want %v", i, at[i], c.want[i])
				}
			}
			if bytes.Contains(got.Data, []byte("Exif")) {
				t.Error("the EXIF segment was kept, and with it an orientation already applied")
			}
		})
	}
}

func TestPrepareConvertsGIFAndWebP(t *testing.T) {
	var gifData bytes.Buffer
	palette := color.Palette{color.Transparent, color.RGBA{R: 255, A: 255}}
	frame := image.NewPaletted(image.Rect(0, 0, 20, 10), palette)
	frame.SetColorIndex(3, 3, 1)
	if err := gif.Encode(&gifData, frame, nil); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name      string
		data      []byte
		wantMedia string
	}{
		{"gif becomes png", gifData.Bytes(), imaging.MediaPNG},
		{"lossy webp becomes jpeg", readFile(t, "testdata/blue-purple-pink.lossy.webp"), imaging.MediaJPEG},
		{"lossless webp becomes png", readFile(t, "testdata/gopher-doc.8bpp.lossless.webp"), imaging.MediaPNG},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := prepare(t, c.data)
			if got.MediaType != c.wantMedia {
				t.Fatalf("media type = %s, want %s", got.MediaType, c.wantMedia)
			}
			if b := decode(t, got).Bounds(); b.Dx() != got.Width || b.Dy() != got.Height {
				t.Errorf("decoded %v, reported %d×%d", b, got.Width, got.Height)
			}
		})
	}
}

func TestPrepareWritesSixteenBitPNGsAsEightBit(t *testing.T) {
	src := image.NewRGBA64(image.Rect(0, 0, 32, 32))
	for y := range 32 {
		for x := range 32 {
			src.SetRGBA64(x, y, color.RGBA64{R: uint16(x * 2000), G: uint16(y * 2000), B: 0x8000, A: 0xffff})
		}
	}
	got := prepare(t, encodePNG(t, src))
	// Byte 24 of a PNG is the bit depth in its header.
	if got.Data[24] != 8 {
		t.Errorf("bit depth = %d, want 8", got.Data[24])
	}
}

// Noise does not compress, so a PNG of it is over the size limit and has to
// become a JPEG; a transparent one goes on white.
func TestPrepareFallsBackToJPEGForAPNGOverTheSizeLimit(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	src := image.NewNRGBA(image.Rect(0, 0, 1920, 1080))
	for i := range src.Pix {
		src.Pix[i] = uint8(rng.UintN(256))
	}
	for i := 3; i < len(src.Pix); i += 4 {
		src.Pix[i] = 255
	}
	src.Pix[3] = 0
	got := prepare(t, encodePNG(t, src))
	if got.MediaType != imaging.MediaJPEG || len(got.Data) > imaging.MaxBytes {
		t.Fatalf("got %s of %d bytes, want image/jpeg within %d", got.MediaType, len(got.Data), imaging.MaxBytes)
	}
	if !near(decode(t, got).At(0, 0), color.RGBA{R: 255, G: 255, B: 255, A: 255}, 64) {
		t.Errorf("the transparent pixel is %v, want it on white", decode(t, got).At(0, 0))
	}
}

func TestPrepareRefusesWhatIsNotAnImageItCanRead(t *testing.T) {
	good := encodePNG(t, solid(10, 10, color.RGBA{A: 255}))
	cases := []struct {
		name    string
		data    []byte
		wantErr error
	}{
		{"empty", nil, nil},
		{"text", []byte("hello, this is not an image"), imaging.ErrUnsupported},
		{"bmp", append([]byte("BM"), make([]byte, 64)...), imaging.ErrUnsupported},
		{"truncated png", good[:len(good)/2], nil},
		{"truncated jpeg", encodeJPEG(t, solid(64, 64, color.RGBA{A: 255}), 90)[:200], nil},
		{"over the pixel limit", pngHeader(10000, 10000), nil},
		{"no pixels", pngHeader(0, 10), nil},
		{"over the size limit", append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, imaging.MaxInputBytes)...), nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := imaging.Prepare(c.data)
			if err == nil {
				t.Fatal("Prepare succeeded, want an error")
			}
			if c.wantErr != nil && !errors.Is(err, c.wantErr) {
				t.Errorf("error = %v, want %v", err, c.wantErr)
			}
		})
	}
}

func prepare(t *testing.T, data []byte) provider.Image {
	t.Helper()
	got, err := imaging.Prepare(data)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if len(got.Data) > imaging.MaxBytes {
		t.Fatalf("Prepare returned %d bytes, over %d", len(got.Data), imaging.MaxBytes)
	}
	return got
}

func decode(t *testing.T, img provider.Image) image.Image {
	t.Helper()
	var (
		out image.Image
		err error
	)
	switch img.MediaType {
	case imaging.MediaPNG:
		out, err = png.Decode(bytes.NewReader(img.Data))
	case imaging.MediaJPEG:
		out, err = jpeg.Decode(bytes.NewReader(img.Data))
	default:
		t.Fatalf("media type %q", img.MediaType)
	}
	if err != nil {
		t.Fatalf("decode the prepared image: %v", err)
	}
	return out
}

func encodePNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func encodeJPEG(t *testing.T, img image.Image, quality int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func readFile(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func solid(w, h int, c color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = c.R, c.G, c.B, c.A
	}
	return img
}

// quadrants is red top-left, green top-right, blue bottom-left, and white
// bottom-right.
func quadrants(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			c := color.RGBA{A: 255}
			switch {
			case x < w/2 && y < h/2:
				c.R = 255
			case y < h/2:
				c.G = 255
			case x < w/2:
				c.B = 255
			default:
				c.R, c.G, c.B = 255, 255, 255
			}
			img.SetRGBA(x, y, c)
		}
	}
	return img
}

// exifOrientation is the payload of an APP1 segment holding a big-endian
// EXIF structure with the orientation tag alone.
func exifOrientation(v uint16) []byte {
	b := []byte("Exif\x00\x00MM\x00\x2a\x00\x00\x00\x08")
	b = binary.BigEndian.AppendUint16(b, 1)
	b = binary.BigEndian.AppendUint16(b, 0x0112)
	b = binary.BigEndian.AppendUint16(b, 3)
	b = binary.BigEndian.AppendUint32(b, 1)
	b = binary.BigEndian.AppendUint16(b, v)
	b = append(b, 0, 0)
	return binary.BigEndian.AppendUint32(b, 0)
}

// withAPP1 inserts an APP1 segment with payload right after a JPEG's start
// marker, where a camera writes its EXIF.
func withAPP1(jpg, payload []byte) []byte {
	out := append([]byte(nil), jpg[:2]...)
	out = append(out, 0xff, 0xe1)
	out = binary.BigEndian.AppendUint16(out, uint16(len(payload)+2))
	out = append(out, payload...)
	return append(out, jpg[2:]...)
}

// pngHeader is a PNG signature and header chunk claiming a size, with no
// pixels after it.
func pngHeader(w, h uint32) []byte {
	chunk := []byte("IHDR")
	chunk = binary.BigEndian.AppendUint32(chunk, w)
	chunk = binary.BigEndian.AppendUint32(chunk, h)
	chunk = append(chunk, 8, 6, 0, 0, 0)
	b := append([]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\x0d"), chunk...)
	return binary.BigEndian.AppendUint32(b, crc32.ChecksumIEEE(chunk))
}

func sameColor(a, b color.Color) bool {
	ar, ag, ab, aa := a.RGBA()
	br, bg, bb, ba := b.RGBA()
	return ar>>8 == br>>8 && ag>>8 == bg>>8 && ab>>8 == bb>>8 && aa>>8 == ba>>8
}

func near(a color.Color, b color.RGBA, tolerance int) bool {
	ar, ag, ab, _ := a.RGBA()
	d := func(x uint32, y uint8) bool {
		diff := int(x>>8) - int(y)
		return diff >= -tolerance && diff <= tolerance
	}
	return d(ar, b.R) && d(ag, b.G) && d(ab, b.B)
}
