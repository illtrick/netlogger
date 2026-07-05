// Command genicon renders the NetLogger app icon (a dark tile with the accent
// pulse line and a healthy-green endpoint dot, matching the app palette) at
// several sizes and packs them into a Windows .ico with PNG-encoded entries.
//
// Regenerate with: go run ./tools/genicon -o cmd/netlogger-app/icon.ico
package main

import (
	"bytes"
	"encoding/binary"
	"flag"
	"image"
	"image/color"
	"image/png"
	"io"
	"log"
	"math"
	"os"
)

var (
	colBG     = color.NRGBA{R: 0x11, G: 0x1A, B: 0x26, A: 0xFF} // title-bar surface
	colAccent = color.NRGBA{R: 0x58, G: 0xA6, B: 0xFF, A: 0xFF}
	colGood   = color.NRGBA{R: 0x3F, G: 0xB9, B: 0x50, A: 0xFF}
)

// pulse is the heartbeat polyline in 0..1 design space (x, y).
var pulse = [][2]float64{
	{0.12, 0.55}, {0.34, 0.55}, {0.44, 0.26}, {0.56, 0.76}, {0.64, 0.55}, {0.86, 0.55},
}

func drawIcon(size int) image.Image {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	s := float64(size)
	radius := s * 0.22 // rounded-corner radius

	// Rounded-square background.
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			fx, fy := float64(x)+0.5, float64(y)+0.5
			if insideRounded(fx, fy, s, radius) {
				img.SetNRGBA(x, y, colBG)
			}
		}
	}

	// Pulse line: stamp discs along each segment for a thick stroke.
	stroke := math.Max(1, s*0.055)
	for i := 0; i+1 < len(pulse); i++ {
		x0, y0 := pulse[i][0]*s, pulse[i][1]*s
		x1, y1 := pulse[i+1][0]*s, pulse[i+1][1]*s
		steps := int(math.Hypot(x1-x0, y1-y0)) * 2
		if steps < 2 {
			steps = 2
		}
		for t := 0; t <= steps; t++ {
			f := float64(t) / float64(steps)
			stampDisc(img, x0+(x1-x0)*f, y0+(y1-y0)*f, stroke, colAccent)
		}
	}

	// Endpoint dot in healthy green.
	stampDisc(img, pulse[len(pulse)-1][0]*s, pulse[len(pulse)-1][1]*s, math.Max(1.5, s*0.085), colGood)
	return img
}

func insideRounded(x, y, s, r float64) bool {
	if x < 0 || y < 0 || x > s || y > s {
		return false
	}
	cx := math.Min(math.Max(x, r), s-r)
	cy := math.Min(math.Max(y, r), s-r)
	return math.Hypot(x-cx, y-cy) <= r || (x >= r && x <= s-r) || (y >= r && y <= s-r)
}

func stampDisc(img *image.NRGBA, cx, cy, r float64, c color.NRGBA) {
	for y := int(cy - r - 1); y <= int(cy+r+1); y++ {
		for x := int(cx - r - 1); x <= int(cx+r+1); x++ {
			if x < 0 || y < 0 || x >= img.Rect.Dx() || y >= img.Rect.Dy() {
				continue
			}
			d := math.Hypot(float64(x)+0.5-cx, float64(y)+0.5-cy)
			if d <= r && img.NRGBAAt(x, y).A > 0 { // keep the pulse inside the tile
				img.SetNRGBA(x, y, c)
			}
		}
	}
}

// encodePNG is the single PNG-encoding path for both the .ico and .icns
// outputs, so an encoder change (compression, color profile) applies to both.
func encodePNG(im image.Image) ([]byte, error) {
	var b bytes.Buffer
	if err := png.Encode(&b, im); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// writeICO packs PNG-encoded images into a .ico (PNG entries are valid Vista+).
func writeICO(path string, imgs []image.Image) error {
	var blobs [][]byte
	for _, im := range imgs {
		blob, err := encodePNG(im)
		if err != nil {
			return err
		}
		blobs = append(blobs, blob)
	}
	var out bytes.Buffer
	le := binary.LittleEndian
	// ICONDIR
	binary.Write(&out, le, uint16(0)) // reserved
	binary.Write(&out, le, uint16(1)) // type: icon
	binary.Write(&out, le, uint16(len(imgs)))
	offset := 6 + 16*len(imgs)
	for i, im := range imgs {
		w := im.Bounds().Dx()
		b := byte(w)
		if w >= 256 {
			b = 0
		}
		out.WriteByte(b)                              // width (0 = 256)
		out.WriteByte(b)                              // height
		out.WriteByte(0)                              // colors
		out.WriteByte(0)                              // reserved
		binary.Write(&out, le, uint16(1))             // planes
		binary.Write(&out, le, uint16(32))            // bit count
		binary.Write(&out, le, uint32(len(blobs[i]))) // bytes in resource
		binary.Write(&out, le, uint32(offset))        // image offset
		offset += len(blobs[i])
	}
	for _, blob := range blobs {
		out.Write(blob)
	}
	return os.WriteFile(path, out.Bytes(), 0o644)
}

// icnsChunk is one icon element: a 4-char OSType and a raw PNG payload.
type icnsChunk struct {
	Type string
	PNG  []byte
}

// writeICNS frames chunks into the ICNS container: "icns" + total length,
// then (type + length + payload) per chunk. Lengths include the 8-byte header.
func writeICNS(w io.Writer, chunks []icnsChunk) error {
	total := 8
	for _, c := range chunks {
		total += 8 + len(c.PNG)
	}
	hdr := make([]byte, 8)
	copy(hdr, "icns")
	binary.BigEndian.PutUint32(hdr[4:], uint32(total))
	if _, err := w.Write(hdr); err != nil {
		return err
	}
	for _, c := range chunks {
		ch := make([]byte, 8)
		copy(ch, c.Type)
		binary.BigEndian.PutUint32(ch[4:], uint32(8+len(c.PNG)))
		if _, err := w.Write(ch); err != nil {
			return err
		}
		if _, err := w.Write(c.PNG); err != nil {
			return err
		}
	}
	return nil
}

// icnsSizes maps each ICNS OSType to its pixel dimension.
var icnsSizes = []struct {
	typ  string
	size int
}{
	{"ic11", 32},   // 16pt@2x
	{"ic12", 64},   // 32pt@2x
	{"ic07", 128},  // 128pt
	{"ic13", 256},  // 128pt@2x
	{"ic08", 256},  // 256pt
	{"ic14", 512},  // 256pt@2x
	{"ic09", 512},  // 512pt
	{"ic10", 1024}, // 512pt@2x
}

func renderPNG(size int) ([]byte, error) {
	return encodePNG(drawIcon(size))
}

func main() {
	out := flag.String("o", "icon.ico", "output .ico path")
	icns := flag.Bool("icns", false, "write an ICNS container instead of .ico")
	flag.Parse()

	if *icns {
		pngBySize := make(map[int][]byte)
		var chunks []icnsChunk
		for _, e := range icnsSizes {
			blob, ok := pngBySize[e.size]
			if !ok {
				var err error
				blob, err = renderPNG(e.size)
				if err != nil {
					log.Fatal(err)
				}
				pngBySize[e.size] = blob
			}
			chunks = append(chunks, icnsChunk{Type: e.typ, PNG: blob})
		}
		f, err := os.Create(*out)
		if err != nil {
			log.Fatal(err)
		}
		defer f.Close()
		if err := writeICNS(f, chunks); err != nil {
			log.Fatal(err)
		}
		log.Printf("wrote %s", *out)
		return
	}

	var imgs []image.Image
	for _, size := range []int{16, 24, 32, 48, 64, 128, 256} {
		imgs = append(imgs, drawIcon(size))
	}
	if err := writeICO(*out, imgs); err != nil {
		log.Fatal(err)
	}
	log.Printf("wrote %s", *out)
}
