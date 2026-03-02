package gui

import (
	"bytes"
	"image"
	"image/color"
	"image/png"

	"fyne.io/fyne/v2"
)

// appIcon generates a simple system tray icon: a coloured square with a white
// "R" drawn using basic pixel art. The icon is 32×32 and encoded as PNG so
// Fyne can use it as a static resource.
func appIcon() fyne.Resource {
	const size = 32
	img := image.NewRGBA(image.Rect(0, 0, size, size))

	// Background: teal/blue.
	bg := color.RGBA{R: 0x00, G: 0x96, B: 0x88, A: 0xFF} // Material Teal 500
	fg := color.RGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF}

	// Fill background with rounded-ish corners (skip the 4 corner pixels).
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			// Skip corners to give a slightly rounded look.
			if (x == 0 || x == size-1) && (y == 0 || y == size-1) {
				continue
			}
			img.SetRGBA(x, y, bg)
		}
	}

	// Draw a simple "R" glyph using filled rectangles.
	// Working in a 32×32 canvas with the letter occupying roughly columns 8-23, rows 5-27.
	fillRect(img, fg, 9, 5, 13, 27)   // Left vertical stroke
	fillRect(img, fg, 13, 5, 22, 9)   // Top horizontal bar
	fillRect(img, fg, 13, 14, 22, 18) // Middle horizontal bar
	fillRect(img, fg, 21, 9, 25, 14)  // Right side of top bowl
	fillRect(img, fg, 17, 18, 25, 27) // Right diagonal leg (simplified as block)

	// Encode to PNG.
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)

	return fyne.NewStaticResource("icon.png", buf.Bytes())
}

// fillRect fills a rectangle on the image.
func fillRect(img *image.RGBA, c color.RGBA, x0, y0, x1, y1 int) {
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			img.SetRGBA(x, y, c)
		}
	}
}
