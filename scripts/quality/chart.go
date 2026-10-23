package main

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
)

var glyphs = map[rune]string{
	'A': "01110100011000111111100011000110001", 'B': "11110100011000111110100011000111110",
	'C': "01111100001000010000100001000001111", 'D': "11110100011000110001100011000111110",
	'E': "11111100001000011110100001000011111", 'F': "11111100001000011110100001000010000",
	'G': "01111100001000010111100011000101111", 'H': "10001100011000111111100011000110001",
	'I': "11111001000010000100001000010011111", 'J': "00111000100001000010100101001001100",
	'K': "10001100101010011000101001001010001", 'L': "10000100001000010000100001000011111",
	'M': "10001110111010110101100011000110001", 'N': "10001110011010110011100011000110001",
	'O': "01110100011000110001100011000101110", 'P': "11110100011000111110100001000010000",
	'Q': "01110100011000110001101011001001101", 'R': "11110100011000111110101001001010001",
	'S': "01111100001000001110000010000111110", 'T': "11111001000010000100001000010000100",
	'U': "10001100011000110001100011000101110", 'V': "10001100011000110001100010101000100",
	'W': "10001100011000110101101011101110001", 'X': "10001100010101000100010101000110001",
	'Y': "10001100010101000100001000010000100", 'Z': "11111000010001000100010001000011111",
	'0': "01110100011001110101110011000101110", '1': "00100011000010000100001000010001110",
	'2': "01110100010000100010001000100011111", '3': "11110000010000101110000010000111110",
	'4': "00010001100101010010111110001000010", '5': "11111100001000011110000010000111110",
	'6': "01110100001000011110100011000101110", '7': "11111000010001000100010000100001000",
	'8': "01110100011000101110100011000101110", '9': "01110100011000101111000010000101110",
	'.': "00000000000000000000000000011000110", ':': "00000001100011000000001100011000000",
	'-': "00000000000000011111000000000000000", '/': "00001000100001000100010000100010000",
	' ': "00000000000000000000000000000000000",
}

func text(img *image.RGBA, x, y, scale int, s string, c color.RGBA, clip image.Rectangle) {
	for _, ch := range s {
		bits, ok := glyphs[ch]
		if !ok {
			bits = glyphs[' ']
		}
		for i, b := range bits {
			if b == '1' {
				r := image.Rect(x+(i%5)*scale, y+(i/5)*scale, x+(i%5+1)*scale, y+(i/5+1)*scale).Intersect(clip)
				draw.Draw(img, r, image.NewUniform(c), image.Point{}, draw.Src)
			}
		}
		x += 6 * scale
	}
}

func regions(w, h int) map[string]image.Rectangle {
	box := func(x, y, bw, bh int) image.Rectangle {
		return image.Rect(x*w/1920, y*h/1080, (x+bw)*w/1920, (y+bh)*h/1080)
	}
	return map[string]image.Rectangle{
		"black-text": box(48, 90, 864, 160), "colored-text": box(1008, 90, 864, 160),
		"one-pixel": box(48, 290, 864, 190), "gradients": box(1008, 290, 864, 190),
		"aspect": box(48, 520, 864, 270), "color-range": box(1008, 520, 864, 270),
		"motion": box(48, 840, 1824, 180),
	}
}

func chart(w, h, frame, frames int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	white, black := color.RGBA{255, 255, 255, 255}, color.RGBA{0, 0, 0, 255}
	red, blue, green := color.RGBA{255, 0, 0, 255}, color.RGBA{0, 0, 255, 255}, color.RGBA{0, 160, 0, 255}
	draw.Draw(img, img.Bounds(), image.NewUniform(white), image.Point{}, draw.Src)
	for x := 0; x < w; x++ {
		img.SetRGBA(x, 0, black)
		img.SetRGBA(x, h-1, black)
	}
	for y := 0; y < h; y++ {
		img.SetRGBA(0, y, black)
		img.SetRGBA(w-1, y, black)
	}
	text(img, w/40, h/40, max(1, w/640), "AIRCAST RGB QUALITY CHART - NOT A RECEIVER TEST", black, img.Bounds())
	r := regions(w, h)
	for _, name := range []string{"black-text", "colored-text"} {
		box := r[name]
		for row, y := 0, box.Min.Y; y+7 <= box.Max.Y; row, y = row+1, y+12 {
			c := black
			if name == "colored-text" {
				c = []color.RGBA{red, blue, green}[row%3]
			}
			text(img, box.Min.X, y, 1, "FINE TEXT 1PX RGB 0123456789 ILMW .: / ABCEFG", c, box)
		}
	}
	px := r["one-pixel"]
	for y := px.Min.Y; y < px.Max.Y; y++ {
		for x := px.Min.X; x < px.Max.X; x++ {
			i, j := x-px.Min.X, y-px.Min.Y
			c := white
			switch min(3, i*4/px.Dx()) {
			case 0:
				if i%2 == 0 {
					c = black
				}
			case 1:
				if j%2 == 0 {
					c = black
				}
			case 2:
				if (i+j)%2 == 0 {
					c = black
				}
			case 3:
				c = []color.RGBA{red, blue, green}[i%3]
			}
			img.SetRGBA(x, y, c)
		}
	}
	gr := r["gradients"]
	for y := gr.Min.Y; y < gr.Max.Y; y++ {
		for x := gr.Min.X; x < gr.Max.X; x++ {
			v := uint8((x - gr.Min.X) * 255 / (gr.Dx() - 1))
			c := color.RGBA{v, v, v, 255}
			switch min(3, (y-gr.Min.Y)*4/gr.Dy()) {
			case 1:
				c = color.RGBA{v, 0, 0, 255}
			case 2:
				c = color.RGBA{0, v, 0, 255}
			case 3:
				c = color.RGBA{0, 0, v, 255}
			}
			img.SetRGBA(x, y, c)
		}
	}
	aspect := r["aspect"]
	cx, cy, radius := aspect.Min.X+aspect.Dx()/2, aspect.Min.Y+aspect.Dy()/2, aspect.Dy()/2-3
	for y := aspect.Min.Y; y < aspect.Max.Y; y++ {
		for x := aspect.Min.X; x < aspect.Max.X; x++ {
			dx, dy := x-cx, y-cy
			d := dx*dx + dy*dy
			if (d <= radius*radius && d >= (radius-2)*(radius-2)) || (d <= radius*radius/4 && d >= (radius/2-2)*(radius/2-2)) || (y == cy && abs(dx) <= radius) || (x == cx && abs(dy) <= radius) {
				img.SetRGBA(x, y, black)
			}
		}
	}
	cr := r["color-range"]
	for y := cr.Min.Y; y < cr.Max.Y; y++ {
		for x := cr.Min.X; x < cr.Max.X; x++ {
			i := (x - cr.Min.X) * 256 / cr.Dx()
			v := uint8(i / 8)
			c := color.RGBA{v, v, v, 255}
			switch min(2, (y-cr.Min.Y)*3/cr.Dy()) {
			case 1:
				v = uint8(224 + i/8)
				c = color.RGBA{v, v, v, 255}
			case 2:
				c = []color.RGBA{white, {255, 255, 0, 255}, {0, 255, 255, 255}, {0, 255, 0, 255}, {255, 0, 255, 255}, red, blue, black}[min(7, i/32)]
			}
			img.SetRGBA(x, y, c)
		}
	}
	motion := r["motion"]
	text(img, motion.Min.X, motion.Min.Y, 1, fmt.Sprintf("FRAME %04d / %04d - MOVING BOX", frame, frames), black, motion)
	marker, cell := frameMarker(w, h)
	for bit := 0; bit < 16; bit++ {
		c := black
		if frame&(1<<bit) != 0 {
			c = white
		}
		draw.Draw(img, image.Rect(marker.Min.X+bit*cell, marker.Min.Y, marker.Min.X+(bit+1)*cell, marker.Max.Y), image.NewUniform(c), image.Point{}, draw.Src)
	}
	size := max(12, motion.Dy()/2)
	x := motion.Min.X + (motion.Dx()-size)*frame/max(1, frames-1)
	y := motion.Min.Y + motion.Dy() - size
	draw.Draw(img, image.Rect(x, y, x+size, y+size), image.NewUniform(red), image.Point{}, draw.Src)
	text(img, x+2, y+2, 1, "RGB", white, image.Rect(x, y, x+size, y+size))
	return img
}

func frameMarker(w, h int) (image.Rectangle, int) {
	motion := regions(w, h)["motion"]
	cell := max(8, w/240)
	return image.Rect(motion.Max.X-16*cell, motion.Min.Y+12, motion.Max.X, motion.Min.Y+12+cell), cell
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
