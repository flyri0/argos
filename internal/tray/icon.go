package tray

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"runtime"
)

const iconSize = 32

// icon returns the tray icon's bytes in whatever format this OS's
// systray.SetIcon expects: an ICO container on Windows, raw PNG
// elsewhere. Argos has no designed logo asset yet, so this is a small,
// plain generated placeholder (a solid rounded square) rather than a
// real icon — worth swapping for actual branding later.
func icon() []byte {
	png := iconPNG()
	if runtime.GOOS == "windows" {
		return iconICO(png, iconSize)
	}
	return png
}

func iconPNG() []byte {
	img := image.NewRGBA(image.Rect(0, 0, iconSize, iconSize))
	fill := color.RGBA{R: 0x2a, G: 0x6f, B: 0x5c, A: 0xff}
	draw.Draw(img, img.Bounds(), &image.Uniform{C: fill}, image.Point{}, draw.Src)

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		// Encoding a freshly-built in-memory RGBA image never fails.
		panic(err)
	}
	return buf.Bytes()
}

// iconICO wraps pngData in a minimal single-image ICO container: a
// 6-byte ICONDIR header plus one 16-byte ICONDIRENTRY, followed directly
// by the PNG bytes — PNG-compressed icon frames have been valid ICO
// content since Windows Vista, so no BMP re-encoding is needed.
func iconICO(pngData []byte, size int) []byte {
	header := make([]byte, 22)
	binary.LittleEndian.PutUint16(header[0:2], 0)    // reserved
	binary.LittleEndian.PutUint16(header[2:4], 1)    // type: icon
	binary.LittleEndian.PutUint16(header[4:6], 1)    // image count
	header[6] = byte(size)                           // width (0 means 256)
	header[7] = byte(size)                           // height
	header[8] = 0                                    // color count
	header[9] = 0                                    // reserved
	binary.LittleEndian.PutUint16(header[10:12], 1)  // color planes
	binary.LittleEndian.PutUint16(header[12:14], 32) // bits per pixel
	binary.LittleEndian.PutUint32(header[14:18], uint32(len(pngData)))
	binary.LittleEndian.PutUint32(header[18:22], 22) // offset to image data

	return append(header, pngData...)
}
