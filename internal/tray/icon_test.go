package tray

import (
	"bytes"
	"encoding/binary"
	"image/png"
	"runtime"
	"testing"
)

func TestIconPNG_IsValidPNG(t *testing.T) {
	data := iconPNG()
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("expected valid PNG, got decode error: %v", err)
	}
	b := img.Bounds()
	if b.Dx() != iconSize || b.Dy() != iconSize {
		t.Fatalf("expected a %dx%d image, got %dx%d", iconSize, iconSize, b.Dx(), b.Dy())
	}
}

func TestIconICO_HasValidHeader(t *testing.T) {
	png := iconPNG()
	ico := iconICO(png, iconSize)

	if len(ico) != 22+len(png) {
		t.Fatalf("expected header + PNG data, got %d bytes for a %d-byte PNG", len(ico), len(png))
	}
	if binary.LittleEndian.Uint16(ico[0:2]) != 0 {
		t.Fatalf("expected reserved field 0")
	}
	if binary.LittleEndian.Uint16(ico[2:4]) != 1 {
		t.Fatalf("expected type 1 (icon)")
	}
	if binary.LittleEndian.Uint16(ico[4:6]) != 1 {
		t.Fatalf("expected image count 1")
	}
	if ico[6] != iconSize || ico[7] != iconSize {
		t.Fatalf("expected width/height %d, got %d/%d", iconSize, ico[6], ico[7])
	}
	if binary.LittleEndian.Uint32(ico[14:18]) != uint32(len(png)) {
		t.Fatalf("expected bytesInRes to equal the PNG length")
	}
	if binary.LittleEndian.Uint32(ico[18:22]) != 22 {
		t.Fatalf("expected the image offset to be 22 (6-byte ICONDIR + 16-byte ICONDIRENTRY)")
	}
	if !bytes.Equal(ico[22:], png) {
		t.Fatalf("expected the PNG bytes to follow the header unmodified")
	}
}

func TestIcon_MatchesFormatForThisOS(t *testing.T) {
	data := icon()
	if runtime.GOOS == "windows" {
		if !bytes.HasPrefix(data, []byte{0, 0, 1, 0}) {
			t.Fatalf("expected an ICO magic header on windows")
		}
		return
	}
	if _, err := png.Decode(bytes.NewReader(data)); err != nil {
		t.Fatalf("expected raw PNG on %s, got decode error: %v", runtime.GOOS, err)
	}
}
