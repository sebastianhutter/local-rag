// gen-icon generates a 512×512 PNG icon for the local-rag macOS app bundle.
// It uses the same teal-"R" design as internal/gui/icon.go but at higher resolution.
//
// Usage: go run ./scripts/gen-icon
package main

import (
	"image"
	"image/color"
	"image/png"
	"log"
	"os"
)

func main() {
	const size = 512
	img := image.NewRGBA(image.Rect(0, 0, size, size))

	bg := color.RGBA{R: 0x00, G: 0x96, B: 0x88, A: 0xFF} // Material Teal 500
	fg := color.RGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF}

	// Corner radius in pixels (scaled from 1px at 32×32).
	const cr = 40

	// Fill background with rounded corners.
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			// Check the four corners for rounding.
			if inCorner(x, y, size, cr) {
				continue
			}
			img.SetRGBA(x, y, bg)
		}
	}

	// Draw the "R" glyph, scaled from the 32×32 version (multiply coords by 16).
	// Original 32×32 coordinates → 512×512:
	//   Left vertical stroke:  (9,5)-(13,27)   → (144,80)-(208,432)
	//   Top horizontal bar:    (13,5)-(22,9)    → (208,80)-(352,144)
	//   Middle horizontal bar: (13,14)-(22,18)  → (208,224)-(352,288)
	//   Right side of bowl:    (21,9)-(25,14)   → (336,144)-(400,224)
	//   Right diagonal leg:    (17,18)-(25,27)  → (272,288)-(400,432)
	fillRect(img, fg, 144, 80, 208, 432)  // Left vertical stroke
	fillRect(img, fg, 208, 80, 352, 144)  // Top horizontal bar
	fillRect(img, fg, 208, 224, 352, 288) // Middle horizontal bar
	fillRect(img, fg, 336, 144, 400, 224) // Right side of top bowl
	fillRect(img, fg, 272, 288, 400, 432) // Right diagonal leg

	f, err := os.Create("Icon.png")
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()

	if err := png.Encode(f, img); err != nil {
		log.Fatal(err)
	}
	log.Println("wrote Icon.png (512×512)")
}

// inCorner returns true if (x,y) falls outside the rounded corner radius.
func inCorner(x, y, size, cr int) bool {
	// Top-left
	if x < cr && y < cr {
		dx, dy := cr-x-1, cr-y-1
		return dx*dx+dy*dy > cr*cr
	}
	// Top-right
	if x >= size-cr && y < cr {
		dx, dy := x-(size-cr), cr-y-1
		return dx*dx+dy*dy > cr*cr
	}
	// Bottom-left
	if x < cr && y >= size-cr {
		dx, dy := cr-x-1, y-(size-cr)
		return dx*dx+dy*dy > cr*cr
	}
	// Bottom-right
	if x >= size-cr && y >= size-cr {
		dx, dy := x-(size-cr), y-(size-cr)
		return dx*dx+dy*dy > cr*cr
	}
	return false
}

func fillRect(img *image.RGBA, c color.RGBA, x0, y0, x1, y1 int) {
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			img.SetRGBA(x, y, c)
		}
	}
}
