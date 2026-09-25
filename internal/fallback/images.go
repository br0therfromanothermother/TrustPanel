package fallback

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"strconv"
	"strings"
)

// Procedural placeholder imagery for the camouflage site: deterministic,
// seed-derived PNGs (network-graph hero art, thumbnails, favicon) so the
// fallback ships no binary assets. Split out of server.go; pure drawing, no
// handler state.

func pngAssetID(name string) (string, bool) {
	name = strings.TrimSpace(name)
	if !strings.HasSuffix(name, ".png") {
		return "", false
	}
	id := strings.TrimSuffix(name, ".png")
	if id == "" || strings.Contains(id, "/") {
		return "", false
	}
	return id, true
}

func assetByID(id string) (portalAsset, bool) {
	id = strings.TrimSpace(id)
	for _, item := range portalAssets {
		if item.ID == id {
			return item, true
		}
	}
	return portalAsset{}, false
}

func writePNG(w http.ResponseWriter, r *http.Request, seed string, width int, height int, maxAge int) {
	sum := sha256.Sum256([]byte(seed + strconv.Itoa(width) + strconv.Itoa(height)))
	etag := `"` + hex.EncodeToString(sum[:8]) + `"`
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("ETag", etag)
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d", maxAge))
	w.Header().Set("Accept-Ranges", "bytes")

	img := image.NewRGBA(image.Rect(0, 0, width, height))
	top := color.RGBA{R: 10 + sum[0]%12, G: 26 + sum[1]%14, B: 46 + sum[2]%18, A: 255}
	bottom := color.RGBA{R: 20 + sum[3]%16, G: 58 + sum[4]%22, B: 88 + sum[5]%24, A: 255}
	for y := 0; y < height; y++ {
		t := y * 255 / maxInt(1, height-1)
		for x := 0; x < width; x++ {
			r := int(top.R)*(255-t)/255 + int(bottom.R)*t/255
			g := int(top.G)*(255-t)/255 + int(bottom.G)*t/255
			b := int(top.B)*(255-t)/255 + int(bottom.B)*t/255
			if (x+y+int(sum[6]))%167 < 2 {
				r += 14
				g += 18
				b += 22
			}
			img.Set(x, y, color.RGBA{R: uint8(minInt(r, 255)), G: uint8(minInt(g, 255)), B: uint8(minInt(b, 255)), A: 255})
		}
	}
	grid := color.RGBA{R: 92, G: 135, B: 165, A: 38}
	for x := width / 12; x < width; x += maxInt(60, width/12) {
		drawRect(img, x, 0, 1, height, grid)
	}
	for y := height / 8; y < height; y += maxInt(54, height/8) {
		drawRect(img, 0, y, width, 1, grid)
	}
	nodes := []image.Point{
		{X: width * 16 / 100, Y: height * 35 / 100},
		{X: width * 31 / 100, Y: height * 22 / 100},
		{X: width * 43 / 100, Y: height * 46 / 100},
		{X: width * 58 / 100, Y: height * 28 / 100},
		{X: width * 71 / 100, Y: height * 52 / 100},
		{X: width * 84 / 100, Y: height * 38 / 100},
		{X: width * 24 / 100, Y: height * 68 / 100},
		{X: width * 62 / 100, Y: height * 72 / 100},
	}
	edges := [][2]int{{0, 1}, {1, 2}, {2, 3}, {3, 4}, {4, 5}, {2, 6}, {4, 7}, {6, 7}, {1, 3}, {3, 5}}
	for _, edge := range edges {
		drawLine(img, nodes[edge[0]], nodes[edge[1]], color.RGBA{R: 87, G: 190, B: 210, A: 94}, maxInt(2, width/700))
	}
	for i, node := range nodes {
		radius := maxInt(10, width/95)
		if i%3 == 0 {
			radius = maxInt(12, width/82)
		}
		drawCircle(img, node, radius+10, color.RGBA{R: 73, G: 176, B: 190, A: 36})
		drawCircle(img, node, radius, color.RGBA{R: 40, G: 163, B: 174, A: 210})
		drawCircle(img, node, maxInt(4, radius/3), color.RGBA{R: 236, G: 255, B: 250, A: 240})
	}
	if strings.Contains(seed, "platform") || strings.Contains(seed, "overview") {
		panelW := width * 28 / 100
		panelH := height * 22 / 100
		drawPanel(img, width*62/100, height*13/100, panelW, panelH, sum[7])
		drawPanel(img, width*8/100, height*55/100, panelW, panelH, sum[8])
	}
	_ = png.Encode(w, img)
}

func drawPanel(img *image.RGBA, x int, y int, w int, h int, seed byte) {
	drawRect(img, x, y, w, h, color.RGBA{R: 244, G: 251, B: 250, A: 32})
	drawRect(img, x, y, w, 2, color.RGBA{R: 245, G: 255, B: 255, A: 72})
	rowH := maxInt(7, h/7)
	for i := 1; i < 5; i++ {
		rowY := y + i*rowH + int(seed%5)
		drawRect(img, x+w/10, rowY, w*7/10, 2, color.RGBA{R: 186, G: 231, B: 229, A: 70})
	}
}

func drawLine(img *image.RGBA, a image.Point, b image.Point, c color.RGBA, thickness int) {
	dx := absInt(b.X - a.X)
	dy := -absInt(b.Y - a.Y)
	sx := -1
	if a.X < b.X {
		sx = 1
	}
	sy := -1
	if a.Y < b.Y {
		sy = 1
	}
	err := dx + dy
	for {
		drawCircle(img, a, thickness, c)
		if a.X == b.X && a.Y == b.Y {
			return
		}
		e2 := 2 * err
		if e2 >= dy {
			err += dy
			a.X += sx
		}
		if e2 <= dx {
			err += dx
			a.Y += sy
		}
	}
}

func drawCircle(img *image.RGBA, center image.Point, radius int, c color.RGBA) {
	r2 := radius * radius
	for y := center.Y - radius; y <= center.Y+radius; y++ {
		for x := center.X - radius; x <= center.X+radius; x++ {
			dx := x - center.X
			dy := y - center.Y
			if dx*dx+dy*dy <= r2 {
				blendPixel(img, x, y, c)
			}
		}
	}
}

func drawRect(img *image.RGBA, x int, y int, w int, h int, c color.RGBA) {
	for yy := y; yy < y+h; yy++ {
		for xx := x; xx < x+w; xx++ {
			blendPixel(img, xx, yy, c)
		}
	}
}

func blendPixel(img *image.RGBA, x int, y int, c color.RGBA) {
	if !image.Pt(x, y).In(img.Bounds()) {
		return
	}
	dst := img.RGBAAt(x, y)
	a := int(c.A)
	inv := 255 - a
	img.SetRGBA(x, y, color.RGBA{
		R: uint8((int(c.R)*a + int(dst.R)*inv) / 255),
		G: uint8((int(c.G)*a + int(dst.G)*inv) / 255),
		B: uint8((int(c.B)*a + int(dst.B)*inv) / 255),
		A: 255,
	})
}

func maxInt(a int, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a int, b int) int {
	if a < b {
		return a
	}
	return b
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func writeIcon(w http.ResponseWriter, seed string, size int, maxAge int) {
	sum := sha256.Sum256([]byte(seed + strconv.Itoa(size)))
	etag := `"` + hex.EncodeToString(sum[:8]) + `"`
	w.Header().Set("ETag", etag)
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d", maxAge))
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	bg := color.RGBA{R: 36, G: 67, B: 111, A: 255}
	fg := color.RGBA{R: 229, G: 236, B: 245, A: 255}
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			if x > size/4 && x < size*3/4 && y > size/4 && y < size*3/4 {
				img.Set(x, y, fg)
				continue
			}
			img.Set(x, y, bg)
		}
	}
	_ = png.Encode(w, img)
}
