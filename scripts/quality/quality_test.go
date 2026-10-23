package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func testConfig(out string) config {
	return config{Out: out, Width: 640, Height: 360, TargetWidth: 640, TargetHeight: 360, Frames: 4, FPS: 30, FFmpeg: "ffmpeg", FFprobe: "ffprobe"}
}

func TestConfiguration(t *testing.T) {
	valid := testConfig("out")
	if err := validate(valid); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		change func(*config)
	}{
		{"aspect", func(c *config) { c.TargetHeight = 352 }},
		{"upscale", func(c *config) { c.TargetWidth = 1280; c.TargetHeight = 720 }},
		{"odd-source", func(c *config) { c.Width = 641 }},
		{"odd-target", func(c *config) { c.TargetWidth = 639 }},
		{"false-4k", func(c *config) { c.Width = 3840; c.Height = 2160; c.TargetWidth = 3840; c.TargetHeight = 2160 }},
		{"frame-count", func(c *config) { c.Frames = 1 }},
		{"frame-rate", func(c *config) { c.FPS = 60 }},
		{"missing-output", func(c *config) { c.Out = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := valid
			tc.change(&c)
			if validate(c) == nil {
				t.Fatal("invalid configuration accepted")
			}
		})
	}
	c := valid
	c.Width = 3840
	c.Height = 2160
	c.TargetWidth = 1920
	c.TargetHeight = 1080
	if err := validate(c); err != nil {
		t.Fatal("large source downscale:", err)
	}
}

func TestStreamValidation(t *testing.T) {
	valid := stream{Codec: "h264", Width: 640, Height: 360, PixelFormat: "yuv420p", SampleAspect: "1:1", DisplayAspect: "16:9", Range: "tv", Space: "bt709", Primaries: "bt709", Transfer: "bt709", Frames: "4", Rate: "30/1", Profile: "High", Level: 40, ChromaLocation: "left"}
	if err := validateStream(valid, 640, 360, 4, 30, true); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		change func(*stream)
	}{
		{"dimensions", func(s *stream) { s.Width = 638 }},
		{"sar", func(s *stream) { s.SampleAspect = "2:1" }},
		{"dar", func(s *stream) { s.DisplayAspect = "4:3" }},
		{"missing-frames", func(s *stream) { s.Frames = "3" }},
		{"cadence", func(s *stream) { s.Rate = "25/1" }},
		{"matrix", func(s *stream) { s.Space = "smpte170m" }},
		{"range", func(s *stream) { s.Range = "pc" }},
		{"chroma", func(s *stream) { s.ChromaLocation = "center" }},
		{"bframes", func(s *stream) { s.BFrames = 1 }},
		{"profile", func(s *stream) { s.Profile = "High 4:4:4 Predictive" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := valid
			tc.change(&s)
			if validateStream(s, 640, 360, 4, 30, true) == nil {
				t.Fatal("invalid stream accepted")
			}
		})
	}
}

func TestChartDeterminism(t *testing.T) {
	first := chart(640, 360, 0, 60)
	if !bytes.Equal(first.Pix, chart(640, 360, 0, 60).Pix) {
		t.Fatal("chart is not deterministic")
	}
	if bytes.Equal(first.Pix, chart(640, 360, 1, 60).Pix) {
		t.Fatal("motion is missing")
	}
	for ch, bits := range glyphs {
		if len(bits) != 35 || strings.Trim(bits, "01") != "" {
			t.Fatalf("invalid glyph %q", ch)
		}
	}
	for _, box := range scaledRegions(640, 360, 320, 180) {
		if box.Dx() < 8 || box.Dy() < 8 || box.Min.X%2 != 0 || box.Min.Y%2 != 0 || box.Dx()%2 != 0 || box.Dy()%2 != 0 {
			t.Fatal("invalid crop", box)
		}
	}
}

func chartRGB(w, h, frame, frames int) []byte {
	img := chart(w, h, frame, frames)
	data := make([]byte, w*h*3)
	for i, j := 0, 0; i < len(data); i, j = i+3, j+4 {
		copy(data[i:i+3], img.Pix[j:j+3])
	}
	return data
}

func TestBlackAndFrameIDGuards(t *testing.T) {
	if validateFrame(make([]byte, 300)) == nil {
		t.Fatal("black frame accepted")
	}
	if validateFrame(bytes.Repeat([]byte{2}, 300)) == nil {
		t.Fatal("near-black frame accepted")
	}
	for _, frame := range []int{0, 1, 30, 59, 299} {
		data := chartRGB(640, 360, frame, 300)
		if err := validateFrame(data); err != nil {
			t.Fatal(err)
		}
		if err := validateFrameID(data, 640, 360, 640, 360, frame); err != nil {
			t.Fatal(err)
		}
		if validateFrameID(data, 640, 360, 640, 360, frame+1) == nil {
			t.Fatal("frozen/misordered frame accepted")
		}
	}
}

func TestErrorMeasurement(t *testing.T) {
	var a accumulator
	a.add([]byte{0, 10, 20, 20, 30, 40}, []byte{10, 20, 30, 30, 40, 50})
	m := a.result()
	for _, v := range []float64{m.RGBMAE, m.RGBRMSE, m.LumaMAE, m.LumaRMSE, m.LumaMax} {
		if math.Abs(v-10) > 1e-9 {
			t.Fatalf("wrong error: %+v", m)
		}
	}
	if m.RGBMax != 10 {
		t.Fatal(m)
	}
	var zero accumulator
	zero.add([]byte{1, 2, 3}, []byte{1, 2, 3})
	if zero.result() != (errorMetrics{}) {
		t.Fatal("identical images differ")
	}
}

func TestDecodedColorGuards(t *testing.T) {
	data := chartRGB(640, 360, 0, 4)
	if err := validateDecodedColors(data, 640, 360, 640, 360); err != nil {
		t.Fatal(err)
	}
	swapped := bytes.Clone(data)
	for i := 0; i < len(swapped); i += 3 {
		swapped[i], swapped[i+2] = swapped[i+2], swapped[i]
	}
	if validateDecodedColors(swapped, 640, 360, 640, 360) == nil {
		t.Fatal("decoded red/blue swap accepted")
	}
	if validateDecodedColors(make([]byte, len(data)), 640, 360, 640, 360) == nil {
		t.Fatal("decoded black color patches accepted")
	}
}

func TestMetricParsing(t *testing.T) {
	for _, psnr := range []string{"42.123456", "inf"} {
		out := "[Parsed_psnr_0] PSNR y:42 u:43 v:44 average:" + psnr + " min:40 max:45\n[Parsed_ssim_0] SSIM Y:0.99 U:0.98 V:0.97 All:0.987654 (19.0)\n"
		got, ssim, _, _, err := parseMetrics(out)
		if err != nil || got != psnr || ssim != 0.987654 {
			t.Fatalf("%s: %s %f %v", psnr, got, ssim, err)
		}
	}
	for _, bad := range []string{"", "PSNR average:nan\nSSIM All:0.9", "PSNR average:40\nSSIM All:nan", "PSNR average:40\nSSIM All:1.1"} {
		if _, _, _, _, err := parseMetrics(bad); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
}

func TestIndependentPSNRAgreement(t *testing.T) {
	if err := checkRGBPSNR("inf", 0); err != nil {
		t.Fatal(err)
	}
	if err := checkRGBPSNR("48.130804", 1); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		psnr string
		rmse float64
	}{{"40", 1}, {"inf", 1}, {"nan", 1}, {"48", 0}} {
		if checkRGBPSNR(tc.psnr, tc.rmse) == nil {
			t.Fatalf("accepted mismatched metrics %+v", tc)
		}
	}
}

func TestEffectiveEncoderUnits(t *testing.T) {
	for _, p := range profiles {
		maxRate, _ := strconv.Atoi(strings.TrimSuffix(p.MaxRate, "M"))
		buf, _ := strconv.Atoi(strings.TrimSuffix(p.Buffer, "k"))
		if buf != maxRate*p.CapacityMS {
			t.Fatal("incorrect VBV bit/millisecond conversion", p)
		}
		output := fmt.Sprintf("rc=crf crf=%d.0 vbv_maxrate=%d vbv_bufsize=%d bframes=0 keyint=60 threads=2", p.CRF, maxRate*1000, buf)
		if err := checkEncoderSettings(output, p, 60); err != nil {
			t.Fatal(err)
		}
		if checkEncoderSettings(strings.ReplaceAll(output, "vbv_bufsize=", "wrong_buffer="), p, 60) == nil {
			t.Fatal("missing VBV accepted")
		}
	}
}

func TestMetricFrameCounts(t *testing.T) {
	for _, tc := range []struct {
		name, log string
		wantError bool
	}{
		{"valid", "n:1 mse_avg:1\nn:2 mse_avg:2\n", false},
		{"truncated", "n:1 mse_avg:1\n", true},
		{"duplicate", "n:1 mse_avg:1\nn:1 mse_avg:1\n", true},
		{"unordered", "n:2 mse_avg:1\nn:1 mse_avg:1\n", true},
		{"malformed", "garbage\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "metrics.log")
			if err := os.WriteFile(path, []byte(tc.log), 0644); err != nil {
				t.Fatal(err)
			}
			if err := checkMetricFrames(path, 2); (err != nil) != tc.wantError {
				t.Fatalf("got %v", err)
			}
		})
	}
}

func TestConversionOracle(t *testing.T) {
	for _, tc := range []struct {
		rgb [3]float64
		yuv [3]int
	}{
		{[3]float64{0, 0, 0}, [3]int{16, 128, 128}},
		{[3]float64{1, 1, 1}, [3]int{235, 128, 128}},
		{[3]float64{1, 0, 0}, [3]int{63, 102, 240}},
		{[3]float64{0, 1, 0}, [3]int{173, 42, 26}},
		{[3]float64{0, 0, 1}, [3]int{32, 240, 118}},
	} {
		if got := yuv709(tc.rgb[0], tc.rgb[1], tc.rgb[2]); got != tc.yuv {
			t.Fatalf("got %v want %v", got, tc.yuv)
		}
	}
}

func TestReadTruncatedFrame(t *testing.T) {
	if _, err := readFrame(bytes.NewReader([]byte{1, 2}), 3); err != io.ErrUnexpectedEOF {
		t.Fatal(err)
	}
}

func TestFailedCommandProducesFailureReport(t *testing.T) {
	c := testConfig(filepath.Join(t.TempDir(), "failed"))
	c.FFmpeg = filepath.Join(t.TempDir(), "missing-ffmpeg")
	if execute(context.Background(), c) == nil {
		t.Fatal("missing command accepted")
	}
	data, err := os.ReadFile(filepath.Join(c.Out, "results.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got report
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.Status != "failed" || got.Error == "" || len(got.Commands) != 1 || got.Commands[0].Error == "" {
		t.Fatalf("invalid failure report: %+v", got)
	}
	html, err := os.ReadFile(filepath.Join(c.Out, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(html, []byte("FAILED")) {
		t.Fatal("HTML concealed failure")
	}
}

func TestNonzeroCommandIsNotSuccess(t *testing.T) {
	r := &runner{ctx: context.Background(), cfg: testConfig(t.TempDir()), report: &report{}}
	if _, err := r.command(os.Args[0], "-unknown-quality-test-flag"); err == nil {
		t.Fatal("nonzero command accepted")
	}
	if len(r.report.Commands) != 1 || r.report.Commands[0].Error == "" {
		t.Fatal("command failure not recorded")
	}
}

func TestExistingOutputIsPreserved(t *testing.T) {
	c := testConfig(t.TempDir())
	path := filepath.Join(c.Out, "keep.txt")
	if err := os.WriteFile(path, []byte("user work"), 0644); err != nil {
		t.Fatal(err)
	}
	if execute(context.Background(), c) == nil {
		t.Fatal("nonempty output accepted")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "user work" {
		t.Fatal("existing output was overwritten")
	}
}

func TestHTMLIsEscaped(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index.html")
	if err := writeHTML(path, &report{Error: "<script>alert(1)</script>", Config: testConfig("out")}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("<script>")) {
		t.Fatal("unescaped error in report")
	}
}

func TestIntegration(t *testing.T) {
	if os.Getenv("AIRCAST_QUALITY_INTEGRATION") != "1" {
		t.Skip("set AIRCAST_QUALITY_INTEGRATION=1 for mandatory FFmpeg integration")
	}
	c := testConfig(filepath.Join(t.TempDir(), "decoded"))
	if err := execute(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(c.Out, "results.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got report
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.Status != "mechanical-checks-passed" || len(got.Comparisons) != 8 || len(got.YUVOracle) != 8 {
		t.Fatalf("incomplete metrics: %+v", got)
	}
	if got.Comparisons[0].Errors != (errorMetrics{}) || got.Comparisons[0].FFmpeg.PSNRDB != "inf" {
		t.Fatal("native scale stage was not lossless")
	}
	r := &runner{ctx: context.Background(), cfg: testConfig(t.TempDir()), report: &report{Streams: map[string]stream{}, FileBytes: map[string]int64{}}}
	if _, err := r.ffmpeg("-f", "lavfi", "-i", "color=black:s=640x360:r=30", "-frames:v", "1", "-c:v", "not-a-real-encoder", "-f", "null", "-"); err == nil {
		t.Fatal("invalid encoder command accepted")
	}
	if _, err := r.ffmpeg("-f", "lavfi", "-i", "color=black:s=640x360:r=30", "-frames:v", "1", "-pix_fmt", "rgb24", "-c:v", "rawvideo", "-threads", "2", "-f", "rawvideo", "black.rgb"); err != nil {
		t.Fatal(err)
	}
	black, err := os.ReadFile(filepath.Join(r.cfg.Out, "black.rgb"))
	if err != nil {
		t.Fatal(err)
	}
	if validateFrame(black) == nil {
		t.Fatal("real FFmpeg black frame accepted")
	}
	if _, err := r.ffmpeg("-f", "lavfi", "-i", "testsrc2=s=640x360:r=30", "-frames:v", "4", "-vf", "setsar=2", "-c:v", "ffv1", "-threads", "2", "aspect-bad.mkv"); err != nil {
		t.Fatal(err)
	}
	if err := r.checkStream("aspect-bad.mkv", false); err == nil {
		t.Fatal("real changed-aspect stream accepted")
	}
	if _, err := os.Stat(filepath.Join(c.Out, "review", "quality-12m-total-frame-0000-colored-text-diff-x4.png")); err != nil {
		t.Fatal(err)
	}
	if !image.Rect(0, 0, 640, 360).Eq(rgbImage(chartRGB(640, 360, 0, 4), 640, 360).Bounds()) {
		t.Fatal("invalid review PNG size")
	}
}
