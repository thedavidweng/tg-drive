//go:build ignore

// Command photo writes a real, decodable JPEG for the preview's seeded
// photos: a drawn landscape (sky gradient, sun, two hill ridges) whose
// palette comes from the named scene, so the image preview scene renders
// a recognisable picture. Output is a pure function of the arguments.
//
//	go run ui-preview/photo.go <out.jpg> <dusk|dawn|night>
package main

import (
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"math"
	"os"
)

type palette struct {
	skyTop, skyBottom, sun, far, near color.RGBA
	sunX, sunY                        float64
}

var palettes = map[string]palette{
	"dusk": {
		skyTop: rgb(0x2b, 0x2d, 0x6e), skyBottom: rgb(0xf4, 0x9d, 0x6e), sun: rgb(0xff, 0xe0, 0x8a),
		far: rgb(0x7a, 0x4f, 0x7d), near: rgb(0x2e, 0x24, 0x3f), sunX: 0.68, sunY: 0.52,
	},
	"dawn": {
		skyTop: rgb(0x8e, 0xc5, 0xfc), skyBottom: rgb(0xfd, 0xe2, 0xe4), sun: rgb(0xff, 0xf4, 0xc2),
		far: rgb(0x6f, 0x9e, 0x8f), near: rgb(0x2f, 0x5d, 0x50), sunX: 0.3, sunY: 0.45,
	},
	"night": {
		skyTop: rgb(0x0b, 0x10, 0x2a), skyBottom: rgb(0x2c, 0x3e, 0x73), sun: rgb(0xe8, 0xec, 0xf5),
		far: rgb(0x23, 0x2f, 0x52), near: rgb(0x10, 0x15, 0x2b), sunX: 0.78, sunY: 0.25,
	},
}

func rgb(r, g, b uint8) color.RGBA { return color.RGBA{r, g, b, 0xff} }

func mix(a, b color.RGBA, t float64) color.RGBA {
	l := func(x, y uint8) uint8 { return uint8(float64(x) + (float64(y)-float64(x))*t + 0.5) }
	return rgb(l(a.R, b.R), l(a.G, b.G), l(a.B, b.B))
}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: go run ui-preview/photo.go <out.jpg> <dusk|dawn|night>")
		os.Exit(2)
	}
	p, ok := palettes[os.Args[2]]
	if !ok {
		fmt.Fprintln(os.Stderr, "photo: unknown palette", os.Args[2])
		os.Exit(2)
	}
	const w, h = 1200, 800
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	sunR := 0.09 * h
	for y := 0; y < h; y++ {
		fy := float64(y) / h
		for x := 0; x < w; x++ {
			fx := float64(x) / w
			c := mix(p.skyTop, p.skyBottom, fy)
			d := math.Hypot(fx*w-p.sunX*w, fy*h-p.sunY*h)
			if d < sunR {
				c = p.sun
			} else if d < sunR*2.2 {
				c = mix(c, p.sun, 0.35*(1-(d-sunR)/(sunR*1.2)))
			}
			far := 0.62 + 0.06*math.Sin(fx*7.1+0.4) + 0.03*math.Sin(fx*19.3)
			near := 0.76 + 0.08*math.Sin(fx*4.3+2.1) + 0.02*math.Sin(fx*23.7+1)
			switch {
			case fy > near:
				c = mix(p.near, rgb(0, 0, 0), 0.4*(fy-near))
			case fy > far:
				c = mix(p.far, p.near, 0.5*(fy-far)/(near-far+0.001))
			}
			img.SetRGBA(x, y, c)
		}
	}
	f, err := os.Create(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "photo:", err)
		os.Exit(1)
	}
	if err := jpeg.Encode(f, img, &jpeg.Options{Quality: 85}); err != nil {
		fmt.Fprintln(os.Stderr, "photo:", err)
		os.Exit(1)
	}
	if err := f.Close(); err != nil {
		fmt.Fprintln(os.Stderr, "photo:", err)
		os.Exit(1)
	}
}
