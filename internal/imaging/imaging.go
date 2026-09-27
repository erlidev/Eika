package imaging

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"

	"golang.org/x/image/draw"
	"golang.org/x/image/webp"

	"github.com/erlidev/eika/internal/provider"
)

// The box an image is fitted within: 1080p, the long side at most
// MaxLongSide and the short side at most MaxShortSide, in either
// orientation. A smaller image keeps its size.
const (
	MaxLongSide  = 1920
	MaxShortSide = 1080
)

// MaxInputBytes bounds the file Prepare reads: a large photo straight off a
// camera fits, and nothing an attachment needs is bigger.
const MaxInputBytes = 20 << 20

// MaxPixels bounds the images Prepare decodes. A file states its size in its
// header, so one that would decode into more memory than any photo needs is
// refused before it is decoded.
const MaxPixels = 50_000_000

// MaxBytes bounds the image Prepare returns. Its base64 form stays under the
// 5 MB the strictest hosted endpoints take per image.
const MaxBytes = 3_500_000

// The media types Prepare produces.
const (
	MediaPNG  = "image/png"
	MediaJPEG = "image/jpeg"
)

// jpegQualities are the qualities a JPEG is encoded at, best first. A lower
// one is tried only while the image is still over MaxBytes.
var jpegQualities = []int{90, 85, 80, 70, 60, 50}

// ErrUnsupported is a file in a format Prepare does not read.
var ErrUnsupported = errors.New("unsupported image format: use PNG, JPEG, GIF, or WebP")

// format is an image file format Prepare reads.
type format int

const (
	formatPNG format = iota + 1
	formatJPEG
	formatGIF
	formatWebP
)

// codec reads one format.
type codec struct {
	config func(io.Reader) (image.Config, error)
	decode func(io.Reader) (image.Image, error)
}

// codecs are called by the format sniff finds rather than through the image
// package's registry, so the formats accepted are exactly these whatever
// else the binary links.
var codecs = map[format]codec{
	formatPNG:  {png.DecodeConfig, png.Decode},
	formatJPEG: {jpeg.DecodeConfig, jpeg.Decode},
	formatGIF:  {gif.DecodeConfig, gif.Decode},
	formatWebP: {webp.DecodeConfig, webp.Decode},
}

// sniff names the format of data by its first bytes. The media type a client
// claims is not trusted.
func sniff(data []byte) (format, bool) {
	switch {
	case bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")):
		return formatPNG, true
	case bytes.HasPrefix(data, []byte{0xff, 0xd8, 0xff}):
		return formatJPEG, true
	case bytes.HasPrefix(data, []byte("GIF87a")), bytes.HasPrefix(data, []byte("GIF89a")):
		return formatGIF, true
	case len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return formatWebP, true
	}
	return 0, false
}

// Prepare turns an image file into what a model is sent: upright, fitted
// within the 1080p box, as PNG or JPEG, without metadata. A JPEG that needs
// none of that keeps its compressed data as it is, so it loses nothing to a
// second compression. An animated GIF is its first frame.
//
// A photo stays a JPEG. Anything else becomes a PNG, which keeps the edges of
// text in a screenshot sharp, unless that is over MaxBytes, when it becomes a
// JPEG on white.
func Prepare(data []byte) (provider.Image, error) {
	if len(data) == 0 {
		return provider.Image{}, errors.New("read image: the file is empty")
	}
	if len(data) > MaxInputBytes {
		return provider.Image{}, fmt.Errorf("read image: the file is %d bytes, over the limit of %d", len(data), MaxInputBytes)
	}
	f, ok := sniff(data)
	if !ok {
		return provider.Image{}, ErrUnsupported
	}
	c := codecs[f]
	cfg, err := c.config(bytes.NewReader(data))
	if err != nil {
		return provider.Image{}, fmt.Errorf("read image header: %w", err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return provider.Image{}, errors.New("read image header: the image has no pixels")
	}
	if int64(cfg.Width)*int64(cfg.Height) > MaxPixels {
		return provider.Image{}, fmt.Errorf("read image header: the image is %d by %d pixels, over the limit of %d million",
			cfg.Width, cfg.Height, MaxPixels/1_000_000)
	}
	// The whole file is decoded even when its bytes are kept, so that a
	// truncated or corrupt one is refused here rather than by the endpoint in
	// the middle of a run.
	img, err := c.decode(bytes.NewReader(data))
	if err != nil {
		return provider.Image{}, fmt.Errorf("decode image: %w", err)
	}

	orientation := 1
	if f == formatJPEG {
		orientation = jpegOrientation(data)
	}
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	uw, uh := w, h
	if transposes(orientation) {
		uw, uh = h, w
	}
	fw, fh := Fit(uw, uh)

	if f == formatJPEG && orientation == 1 && fw == w && fh == h && plainJPEG(img) {
		if kept, err := stripJPEG(data); err == nil && len(kept) <= MaxBytes {
			return provider.Image{MediaType: MediaJPEG, Data: kept, Width: w, Height: h}, nil
		}
	}
	out := img
	if fw != uw || fh != uh {
		// Scaling before turning the image upright turns fewer pixels.
		sw, sh := fw, fh
		if transposes(orientation) {
			sw, sh = fh, fw
		}
		out = scale(img, sw, sh)
	}
	if orientation != 1 {
		out = orient(out, orientation)
	}
	return encode(out, f == formatJPEG || (f == formatWebP && webpLossy(data)))
}

// Fit returns the size an image of w by h pixels is scaled to: the largest
// that keeps its aspect ratio and fits within the 1080p box, and w by h
// itself when that already fits. Images are never enlarged.
func Fit(w, h int) (int, int) {
	long, short := MaxLongSide, MaxShortSide
	if h > w {
		long, short = short, long
	}
	scale := min(1, float64(long)/float64(w), float64(short)/float64(h))
	if scale >= 1 {
		return w, h
	}
	return max(1, int(float64(w)*scale+0.5)), max(1, int(float64(h)*scale+0.5))
}

// scale resamples img to w by h pixels with the Catmull-Rom filter. When it
// shrinks an image the filter widens to cover every source pixel, so fine
// detail averages out rather than aliasing into moiré, and its sharpness keeps
// small text legible.
func scale(img image.Image, w, h int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, img.Bounds(), draw.Src, nil)
	return dst
}

// plainJPEG reports whether a decoded JPEG is in a colour model every
// endpoint reads. A CMYK one is converted rather than kept.
func plainJPEG(img image.Image) bool {
	switch img.(type) {
	case *image.YCbCr, *image.Gray:
		return true
	}
	return false
}

// encode writes img as PNG or, for a lossy source or a PNG over MaxBytes, as
// a JPEG at the best quality that fits.
func encode(img image.Image, lossy bool) (provider.Image, error) {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	opaque := isOpaque(img)
	if !lossy || !opaque {
		var buf bytes.Buffer
		if err := png.Encode(&buf, eightBit(img)); err != nil {
			return provider.Image{}, fmt.Errorf("encode png: %w", err)
		}
		if buf.Len() <= MaxBytes {
			return provider.Image{MediaType: MediaPNG, Data: buf.Bytes(), Width: w, Height: h}, nil
		}
	}
	if !opaque {
		img = flatten(img)
	}
	for _, q := range jpegQualities {
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: q}); err != nil {
			return provider.Image{}, fmt.Errorf("encode jpeg: %w", err)
		}
		if buf.Len() <= MaxBytes {
			return provider.Image{MediaType: MediaJPEG, Data: buf.Bytes(), Width: w, Height: h}, nil
		}
	}
	return provider.Image{}, fmt.Errorf("encode image: over %d bytes even as a JPEG at quality %d",
		MaxBytes, jpegQualities[len(jpegQualities)-1])
}

// isOpaque reports whether every pixel of img is opaque, as far as its type
// can tell without looking.
func isOpaque(img image.Image) bool {
	if o, ok := img.(interface{ Opaque() bool }); ok {
		return o.Opaque()
	}
	return false
}

// eightBit returns img with eight bits per channel. A 16-bit PNG would stay
// 16-bit through the encoder, twice the size for detail no model reads.
func eightBit(img image.Image) image.Image {
	switch img.(type) {
	case *image.RGBA64, *image.NRGBA64, *image.Gray16, *image.Alpha16:
		dst := image.NewNRGBA(image.Rect(0, 0, img.Bounds().Dx(), img.Bounds().Dy()))
		draw.Draw(dst, dst.Bounds(), img, img.Bounds().Min, draw.Src)
		return dst
	}
	return img
}

// flatten composes img over white, since a JPEG has no transparency and the
// black a transparent pixel would otherwise become hides dark content.
func flatten(img image.Image) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, img.Bounds().Dx(), img.Bounds().Dy()))
	draw.Draw(dst, dst.Bounds(), image.White, image.Point{}, draw.Src)
	draw.Draw(dst, dst.Bounds(), img, img.Bounds().Min, draw.Over)
	return dst
}

// transposes reports whether an EXIF orientation swaps width and height.
func transposes(orientation int) bool {
	return orientation >= 5 && orientation <= 8
}

// orient turns img upright by its EXIF orientation: 2 to 8 are the mirror
// images and quarter turns of a camera held otherwise than level.
func orient(img image.Image, orientation int) *image.RGBA {
	src, ok := img.(*image.RGBA)
	if !ok {
		src = image.NewRGBA(image.Rect(0, 0, img.Bounds().Dx(), img.Bounds().Dy()))
		draw.Draw(src, src.Bounds(), img, img.Bounds().Min, draw.Src)
	}
	w, h := src.Rect.Dx(), src.Rect.Dy()
	dw, dh := w, h
	if transposes(orientation) {
		dw, dh = h, w
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := range h {
		for x := range w {
			var dx, dy int
			switch orientation {
			case 2:
				dx, dy = w-1-x, y
			case 3:
				dx, dy = w-1-x, h-1-y
			case 4:
				dx, dy = x, h-1-y
			case 5:
				dx, dy = y, x
			case 6:
				dx, dy = h-1-y, x
			case 7:
				dx, dy = h-1-y, w-1-x
			case 8:
				dx, dy = y, w-1-x
			default:
				dx, dy = x, y
			}
			si := src.PixOffset(src.Rect.Min.X+x, src.Rect.Min.Y+y)
			di := dst.PixOffset(dx, dy)
			copy(dst.Pix[di:di+4], src.Pix[si:si+4])
		}
	}
	return dst
}

// webpLossy reports whether a WebP file holds a lossy (VP8) image rather
// than a lossless (VP8L) one. The chunks follow the 12-byte RIFF header; an
// extended file's VP8X chunk comes first and its image chunk later.
func webpLossy(data []byte) bool {
	for i := 12; i+8 <= len(data); {
		switch string(data[i : i+4]) {
		case "VP8 ":
			return true
		case "VP8L":
			return false
		}
		size := int(uint32(data[i+4]) | uint32(data[i+5])<<8 | uint32(data[i+6])<<16 | uint32(data[i+7])<<24)
		if size < 0 || size > len(data) {
			return false
		}
		i += 8 + size + size&1
	}
	return false
}
