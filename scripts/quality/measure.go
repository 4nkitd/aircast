package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type errorMetrics struct {
	RGBMAE   float64 `json:"rgb_mae"`
	RGBRMSE  float64 `json:"rgb_rmse"`
	RGBMax   int     `json:"rgb_max"`
	LumaMAE  float64 `json:"bt709_luma_mae"`
	LumaRMSE float64 `json:"bt709_luma_rmse"`
	LumaMax  float64 `json:"bt709_luma_max"`
}

type accumulator struct {
	rgbAbs, rgbSquared, yAbs, ySquared, yMax float64
	rgbMax                                   int
	pixels                                   int64
}

func (a *accumulator) add(ref, candidate []byte) {
	for i := 0; i < len(ref); i += 3 {
		dr, dg, db := int(candidate[i])-int(ref[i]), int(candidate[i+1])-int(ref[i+1]), int(candidate[i+2])-int(ref[i+2])
		for _, d := range []int{dr, dg, db} {
			a.rgbAbs += float64(abs(d))
			a.rgbSquared += float64(d * d)
			a.rgbMax = max(a.rgbMax, abs(d))
		}
		y := 0.2126*float64(dr) + 0.7152*float64(dg) + 0.0722*float64(db)
		a.yAbs += math.Abs(y)
		a.ySquared += y * y
		a.yMax = max(a.yMax, math.Abs(y))
		a.pixels++
	}
}

func (a accumulator) result() errorMetrics {
	if a.pixels == 0 {
		return errorMetrics{}
	}
	n := float64(a.pixels)
	return errorMetrics{a.rgbAbs / (n * 3), math.Sqrt(a.rgbSquared / (n * 3)), a.rgbMax, a.yAbs / n, math.Sqrt(a.ySquared / n), a.yMax}
}

type filterMetrics struct {
	Domain      string  `json:"domain"`
	PSNRDB      string  `json:"psnr_db"`
	SSIM        float64 `json:"ssim"`
	PSNRSummary string  `json:"psnr_summary"`
	SSIMSummary string  `json:"ssim_summary"`
	PSNRLog     string  `json:"psnr_log"`
	SSIMLog     string  `json:"ssim_log"`
}

type cropMetrics struct {
	Name   string          `json:"name"`
	Bounds image.Rectangle `json:"bounds"`
	Errors errorMetrics    `json:"errors"`
	FFmpeg filterMetrics   `json:"ffmpeg"`
}

type frameErrors struct {
	Frame  int          `json:"frame"`
	Errors errorMetrics `json:"errors"`
}

type comparison struct {
	Name      string        `json:"name"`
	Meaning   string        `json:"meaning"`
	Reference string        `json:"reference"`
	Candidate string        `json:"candidate"`
	Width     int           `json:"width"`
	Height    int           `json:"height"`
	Errors    errorMetrics  `json:"errors"`
	FFmpeg    filterMetrics `json:"ffmpeg"`
	Crops     []cropMetrics `json:"crops"`
	PerFrame  []frameErrors `json:"per_frame"`
}

func rgbFilter(name string, w, h int, format string) string {
	options := "in_range=full:out_range=full"
	if isYUV(name) {
		options = "in_range=limited:out_range=full:in_color_matrix=bt709:in_h_chr_pos=0:in_v_chr_pos=128"
	}
	return fmt.Sprintf("scale=%d:%d:flags=lanczos:%s,format=%s", w, h, options, format)
}

func isYUV(name string) bool { return name != "source-rgb.mkv" && name != "target-rgb.mkv" }

func (r *runner) decodeRGB(name string, w, h int, out string) error {
	_, err := r.ffmpeg("-i", name, "-map", "0:v:0", "-an", "-vf", rgbFilter(name, w, h, "rgb24"), "-fps_mode", "passthrough", "-c:v", "rawvideo", "-threads", "2", "-pix_fmt", "rgb24", "-f", "rawvideo", out)
	return err
}

func (r *runner) measure(c comparison) (comparison, error) {
	aName, bName := c.Name+"-reference.rgb", c.Name+"-candidate.rgb"
	defer os.Remove(filepath.Join(r.cfg.Out, aName))
	defer os.Remove(filepath.Join(r.cfg.Out, bName))
	if err := r.decodeRGB(c.Reference, c.Width, c.Height, aName); err != nil {
		return c, err
	}
	if err := r.decodeRGB(c.Candidate, c.Width, c.Height, bName); err != nil {
		return c, err
	}
	a, err := os.Open(filepath.Join(r.cfg.Out, aName))
	if err != nil {
		return c, err
	}
	defer a.Close()
	b, err := os.Open(filepath.Join(r.cfg.Out, bName))
	if err != nil {
		return c, err
	}
	defer b.Close()
	boxes := scaledRegions(r.cfg.Width, r.cfg.Height, c.Width, c.Height)
	var all accumulator
	byCrop := map[string]*accumulator{"black-text": {}, "colored-text": {}, "one-pixel": {}, "gradients": {}, "aspect": {}, "color-range": {}, "motion": {}}
	for i := 0; i < r.cfg.Frames; i++ {
		reference, e1 := readFrame(a, c.Width*c.Height*3)
		candidate, e2 := readFrame(b, c.Width*c.Height*3)
		if e1 != nil || e2 != nil {
			return c, fmt.Errorf("%s frame %d: truncated decode: %w", c.Name, i, errors.Join(e1, e2))
		}
		if err := validateFrame(candidate); err != nil {
			return c, fmt.Errorf("%s frame %d: %w", c.Name, i, err)
		}
		if err := validateFrameID(candidate, r.cfg.Width, r.cfg.Height, c.Width, c.Height, i); err != nil {
			return c, fmt.Errorf("%s: %w", c.Name, err)
		}
		if err := validateFrameID(reference, r.cfg.Width, r.cfg.Height, c.Width, c.Height, i); err != nil {
			return c, fmt.Errorf("%s reference: %w", c.Name, err)
		}
		if err := validateDecodedColors(candidate, r.cfg.Width, r.cfg.Height, c.Width, c.Height); err != nil {
			return c, fmt.Errorf("%s frame %d: %w", c.Name, i, err)
		}
		var one accumulator
		one.add(reference, candidate)
		all.add(reference, candidate)
		c.PerFrame = append(c.PerFrame, frameErrors{Frame: i, Errors: one.result()})
		for name, acc := range byCrop {
			box := boxes[name]
			for y := box.Min.Y; y < box.Max.Y; y++ {
				start, end := (y*c.Width+box.Min.X)*3, (y*c.Width+box.Max.X)*3
				acc.add(reference[start:end], candidate[start:end])
			}
		}
		if i == 0 || i == r.cfg.Frames/2 || i == r.cfg.Frames-1 {
			if err := r.reviewFrame(c, i, reference, candidate, boxes); err != nil {
				return c, err
			}
		}
	}
	for _, reader := range []io.Reader{a, b} {
		var extra [1]byte
		if n, err := reader.Read(extra[:]); n != 0 || err != io.EOF {
			return c, fmt.Errorf("%s: decoded frames exceed expected count", c.Name)
		}
	}
	c.Errors = all.result()
	if c.Name == "scale" && r.cfg.Width == r.cfg.TargetWidth && r.cfg.Height == r.cfg.TargetHeight && c.Errors.RGBMax != 0 {
		return c, errors.New("native 1:1 RGB reference path changed pixels")
	}
	c.FFmpeg, err = r.filterMetrics(c, "full", image.Rect(0, 0, c.Width, c.Height), c.Errors.RGBRMSE)
	if err != nil {
		return c, err
	}
	for _, name := range []string{"black-text", "colored-text", "one-pixel", "gradients", "aspect", "color-range", "motion"} {
		crop := cropMetrics{Name: name, Bounds: boxes[name], Errors: byCrop[name].result()}
		if name == "black-text" || name == "colored-text" {
			crop.FFmpeg, err = r.filterMetrics(c, name, boxes[name], crop.Errors.RGBRMSE)
			if err != nil {
				return c, err
			}
		}
		c.Crops = append(c.Crops, crop)
	}
	return c, nil
}

func validateFrame(data []byte) error {
	for _, v := range data {
		if v > 2 {
			return nil
		}
	}
	return errors.New("black decoded frame")
}

func validateFrameID(data []byte, sw, sh, w, h, expected int) error {
	marker, cell := frameMarker(sw, sh)
	got := 0
	for bit := 0; bit < 16; bit++ {
		x := (marker.Min.X + bit*cell + cell/2) * w / sw
		y := (marker.Min.Y + cell/2) * h / sh
		i := (y*w + x) * 3
		luma := 0.2126*float64(data[i]) + 0.7152*float64(data[i+1]) + 0.0722*float64(data[i+2])
		if luma >= 127.5 {
			got |= 1 << bit
		}
	}
	if got != expected {
		return fmt.Errorf("decoded frame ID %d, expected %d (freeze, missing/reordered frame, or damaged marker)", got, expected)
	}
	return nil
}

func validateDecodedColors(data []byte, sw, sh, w, h int) error {
	box := regions(sw, sh)["color-range"]
	names := []string{"white", "yellow", "cyan", "green", "magenta", "red", "blue", "black"}
	for patch, expected := range []int{7, 6, 3, 2, 5, 4, 1, 0} {
		x := (box.Min.X + box.Dx()*(2*patch+1)/16) * w / sw
		y := (box.Min.Y + box.Dy()*5/6) * h / sh
		i := (y*w + x) * 3
		got := 0
		for channel := 0; channel < 3; channel++ {
			if data[i+channel] >= 128 {
				got |= 1 << (2 - channel)
			}
		}
		if got != expected {
			return fmt.Errorf("decoded %s patch has wrong RGB color identity: got %v", names[patch], data[i:i+3])
		}
	}
	return nil
}

func scaledRegions(sw, sh, w, h int) map[string]image.Rectangle {
	result := regions(sw, sh)
	for name, box := range result {
		result[name] = image.Rect((box.Min.X*w/sw)&^1, (box.Min.Y*h/sh)&^1, (box.Max.X*w/sw)&^1, (box.Max.Y*h/sh)&^1)
	}
	return result
}

var psnrPattern = regexp.MustCompile(`PSNR [^\n]*average:([0-9.eE+\-]+|inf)[^\n]*`)
var ssimPattern = regexp.MustCompile(`SSIM [^\n]*All:([0-9.eE+\-]+)[^\n]*`)

func parseMetrics(output string) (string, float64, string, string, error) {
	p, s := psnrPattern.FindStringSubmatch(output), ssimPattern.FindStringSubmatch(output)
	if len(p) != 2 || len(s) != 2 {
		return "", 0, "", "", errors.New("FFmpeg did not emit valid SSIM and PSNR summaries")
	}
	ssim, err := strconv.ParseFloat(s[1], 64)
	if err != nil || math.IsNaN(ssim) || math.IsInf(ssim, 0) || ssim < -1 || ssim > 1 {
		return "", 0, "", "", errors.New("invalid SSIM summary")
	}
	if p[1] != "inf" {
		v, err := strconv.ParseFloat(p[1], 64)
		if err != nil || math.IsInf(v, 0) || math.IsNaN(v) {
			return "", 0, "", "", errors.New("invalid PSNR summary")
		}
	}
	return p[1], ssim, p[0], s[0], nil
}

func (r *runner) filterMetrics(c comparison, roi string, box image.Rectangle, rgbRMSE float64) (filterMetrics, error) {
	prefix := "metrics/" + c.Name + "-" + roi
	m := filterMetrics{Domain: "RGB full-range gbrp", PSNRLog: prefix + "-psnr.log", SSIMLog: prefix + "-ssim.log"}
	a, b := "format=gbrp", "format=gbrp"
	native := strings.HasSuffix(c.Name, "-codec")
	inputs := []string{}
	for _, suffix := range []string{"-candidate.rgb", "-reference.rgb"} {
		inputs = append(inputs, "-threads", "2", "-f", "rawvideo", "-pixel_format", "rgb24", "-video_size", fmt.Sprintf("%dx%d", c.Width, c.Height), "-framerate", strconv.Itoa(r.cfg.FPS), "-i", c.Name+suffix)
	}
	if native {
		m.Domain = "Native BT.709 limited-range yuv420p; summary is plane-weighted, see Y/U/V logs"
		a, b = "format=yuv420p", "format=yuv420p"
		inputs = []string{"-threads", "2", "-i", c.Candidate, "-threads", "2", "-i", c.Reference}
	}
	crop := fmt.Sprintf(",crop=%d:%d:%d:%d,settb=AVTB,setpts=N/(%d*TB)", box.Dx(), box.Dy(), box.Min.X, box.Min.Y, r.cfg.FPS)
	filter := fmt.Sprintf("[0:v]%s%s,split=2[dp][ds];[1:v]%s%s,split=2[rp][rs];[dp][rp]psnr=stats_file=%s:shortest=1:repeatlast=0[p];[ds][rs]ssim=stats_file=%s:shortest=1:repeatlast=0[s]", a, crop, b, crop, m.PSNRLog, m.SSIMLog)
	args := append(inputs, "-filter_complex", filter, "-map", "[p]", "-map", "[s]", "-an", "-fps_mode", "passthrough", "-f", "null", "-")
	output, err := r.ffmpeg(args...)
	if err != nil {
		return m, err
	}
	m.PSNRDB, m.SSIM, m.PSNRSummary, m.SSIMSummary, err = parseMetrics(string(output))
	if err != nil {
		return m, err
	}
	if !native {
		if err := checkRGBPSNR(m.PSNRDB, rgbRMSE); err != nil {
			return m, fmt.Errorf("%s %s: %w", c.Name, roi, err)
		}
	}
	for _, name := range []string{m.PSNRLog, m.SSIMLog} {
		if err := checkMetricFrames(filepath.Join(r.cfg.Out, name), r.cfg.Frames); err != nil {
			return m, err
		}
	}
	return m, nil
}

func checkRGBPSNR(psnr string, rmse float64) error {
	if rmse == 0 && psnr == "inf" {
		return nil
	}
	got, err := strconv.ParseFloat(psnr, 64)
	want := 20 * math.Log10(255/rmse)
	if rmse <= 0 || err != nil || math.IsNaN(got) || math.IsInf(got, 0) || math.Abs(got-want) > 0.00001 {
		return fmt.Errorf("Go/FFmpeg RGB PSNR disagreement: RMSE %g predicts %.9f dB, FFmpeg reports %s", rmse, want, psnr)
	}
	return nil
}

func checkMetricFrames(path string, expected int) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	n := 0
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 0 || !strings.HasPrefix(fields[0], "n:") {
			return fmt.Errorf("%s: malformed metric frame", path)
		}
		got, err := strconv.Atoi(strings.TrimPrefix(fields[0], "n:"))
		if err != nil || got != n+1 {
			return fmt.Errorf("%s: missing or unordered metric frame", path)
		}
		n++
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if n != expected {
		return fmt.Errorf("%s: metric frame count %d, expected %d", path, n, expected)
	}
	return nil
}

func rgbImage(data []byte, w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i, j := 0, 0; i < len(data); i, j = i+3, j+4 {
		copy(img.Pix[j:j+3], data[i:i+3])
		img.Pix[j+3] = 255
	}
	return img
}

func (r *runner) reviewFrame(c comparison, frame int, reference, candidate []byte, boxes map[string]image.Rectangle) error {
	prefix := filepath.Join(r.cfg.Out, "review", fmt.Sprintf("%s-frame-%04d", c.Name, frame))
	a, b := rgbImage(reference, c.Width, c.Height), rgbImage(candidate, c.Width, c.Height)
	if err := writePNG(prefix+"-reference.png", a); err != nil {
		return err
	}
	if err := writePNG(prefix+"-decoded.png", b); err != nil {
		return err
	}
	for _, name := range []string{"black-text", "colored-text", "one-pixel", "aspect"} {
		box := boxes[name]
		diff := image.NewRGBA(box)
		for y := box.Min.Y; y < box.Max.Y; y++ {
			for x := box.Min.X; x < box.Max.X; x++ {
				p, q := a.RGBAAt(x, y), b.RGBAAt(x, y)
				diff.SetRGBA(x, y, color.RGBA{uint8(min(255, abs(int(p.R)-int(q.R))*4)), uint8(min(255, abs(int(p.G)-int(q.G))*4)), uint8(min(255, abs(int(p.B)-int(q.B))*4)), 255})
			}
		}
		for suffix, img := range map[string]image.Image{"reference": a.SubImage(box), "decoded": b.SubImage(box), "diff-x4": diff} {
			if err := writePNG(prefix+"-"+name+"-"+suffix+".png", img); err != nil {
				return err
			}
		}
	}
	return nil
}

type patchCheck struct {
	Name     string `json:"name"`
	X        int    `json:"x"`
	Y        int    `json:"y"`
	Expected [3]int `json:"expected_ycbcr"`
	Actual   [3]int `json:"actual_ycbcr"`
}

func yuv709(r, g, b float64) [3]int {
	y := 0.2126*r + 0.7152*g + 0.0722*b
	return [3]int{int(math.Round(16 + 219*y)), int(math.Round(128 + 224*(b-y)/1.8556)), int(math.Round(128 + 224*(r-y)/1.5748))}
}

func (r *runner) checkYUVOracle() error {
	name := "oracle-yuv420.raw"
	defer os.Remove(filepath.Join(r.cfg.Out, name))
	if _, err := r.ffmpeg("-i", "control-420.mkv", "-frames:v", "1", "-pix_fmt", "yuv420p", "-c:v", "rawvideo", "-threads", "2", "-f", "rawvideo", name); err != nil {
		return err
	}
	data, err := os.ReadFile(filepath.Join(r.cfg.Out, name))
	if err != nil {
		return err
	}
	w, h := r.cfg.TargetWidth, r.cfg.TargetHeight
	if len(data) != w*h*3/2 {
		return errors.New("YUV oracle: invalid raw frame size")
	}
	box := regions(r.cfg.Width, r.cfg.Height)["color-range"]
	colors := [][3]float64{{1, 1, 1}, {1, 1, 0}, {0, 1, 1}, {0, 1, 0}, {1, 0, 1}, {1, 0, 0}, {0, 0, 1}, {0, 0, 0}}
	for i, name := range []string{"white", "yellow", "cyan", "green", "magenta", "red", "blue", "black"} {
		x := (box.Min.X + box.Dx()*(2*i+1)/16) * w / r.cfg.Width
		y := (box.Min.Y + box.Dy()*5/6) * h / r.cfg.Height
		uv := (y/2)*(w/2) + x/2
		check := patchCheck{Name: name, X: x, Y: y, Expected: yuv709(colors[i][0], colors[i][1], colors[i][2]), Actual: [3]int{int(data[y*w+x]), int(data[w*h+uv]), int(data[w*h+w*h/4+uv])}}
		r.report.YUVOracle = append(r.report.YUVOracle, check)
		for j := range check.Expected {
			if abs(check.Expected[j]-check.Actual[j]) > 1 {
				return fmt.Errorf("BT.709 limited-range oracle failed at %s: got %v expected %v (+/- 1 rounding code)", name, check.Actual, check.Expected)
			}
		}
	}
	return nil
}

func (r *runner) writeReport() error {
	data, err := json.MarshalIndent(r.report, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(r.cfg.Out, "results.json"), append(data, '\n'), 0644); err != nil {
		return err
	}
	return writeHTML(filepath.Join(r.cfg.Out, "index.html"), r.report)
}
