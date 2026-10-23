package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"image"
	"image/png"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type config struct {
	Out          string `json:"out"`
	Width        int    `json:"source_width"`
	Height       int    `json:"source_height"`
	TargetWidth  int    `json:"target_width"`
	TargetHeight int    `json:"target_height"`
	Frames       int    `json:"frames"`
	FPS          int    `json:"fps"`
	FFmpeg       string `json:"ffmpeg"`
	FFprobe      string `json:"ffprobe"`
}

type commandRecord struct {
	Args    []string `json:"args"`
	Seconds float64  `json:"seconds"`
	Log     string   `json:"log"`
	Error   string   `json:"error,omitempty"`
}

type profile struct {
	Name       string `json:"name"`
	Preset     string `json:"preset"`
	CRF        int    `json:"crf"`
	MaxRate    string `json:"maxrate"`
	Buffer     string `json:"bufsize"`
	CapacityMS int    `json:"vbv_capacity_ms"`
}

var profiles = []profile{{"quality-12m", "faster", 18, "12M", "1800k", 150}, {"quality-16m", "faster", 18, "16M", "2400k", 150}, {"balanced-8m", "veryfast", 20, "8M", "800k", 100}}

type report struct {
	Status        string                 `json:"status"`
	Error         string                 `json:"error,omitempty"`
	Started       string                 `json:"started"`
	Config        config                 `json:"config"`
	FFmpegVersion string                 `json:"ffmpeg_version"`
	FixtureSHA256 []string               `json:"fixture_sha256"`
	Profiles      []profile              `json:"profiles"`
	Comparisons   []comparison           `json:"comparisons"`
	Commands      []commandRecord        `json:"commands"`
	Streams       map[string]stream      `json:"streams"`
	FileBytes     map[string]int64       `json:"file_bytes"`
	YUVOracle     []patchCheck           `json:"yuv_oracle"`
	PacketStats   map[string]packetStats `json:"packet_stats"`
}

type runner struct {
	ctx    context.Context
	cfg    config
	report *report
}

func main() {
	c := config{}
	flag.StringVar(&c.Out, "out", "quality-output", "new or empty artifact directory")
	flag.IntVar(&c.Width, "width", 1920, "source width, even and at least 640")
	flag.IntVar(&c.Height, "height", 1080, "source height, even and at least 360")
	flag.IntVar(&c.TargetWidth, "target-width", 1920, "encoded width, no upscaling")
	flag.IntVar(&c.TargetHeight, "target-height", 1080, "encoded height, same aspect as source")
	flag.IntVar(&c.Frames, "frames", 60, "frame count, at least 2")
	flag.IntVar(&c.FPS, "fps", 30, "frame rate, 1 to 30")
	flag.StringVar(&c.FFmpeg, "ffmpeg", "ffmpeg", "FFmpeg executable")
	flag.StringVar(&c.FFprobe, "ffprobe", "ffprobe", "ffprobe executable")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "unexpected positional arguments")
		os.Exit(2)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := execute(ctx, c); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func validate(c config) error {
	if c.Out == "" || c.FFmpeg == "" || c.FFprobe == "" {
		return errors.New("out, ffmpeg and ffprobe cannot be empty")
	}
	if c.Width < 640 || c.Height < 360 || c.Width > 3840 || c.Height > 2160 || c.Width%2 != 0 || c.Height%2 != 0 {
		return errors.New("source must have even dimensions between 640x360 and 3840x2160")
	}
	if c.TargetWidth < 320 || c.TargetHeight < 180 || c.TargetWidth > c.Width || c.TargetHeight > c.Height || c.TargetWidth%2 != 0 || c.TargetHeight%2 != 0 {
		return errors.New("target must have even dimensions, at least 320x180, and cannot upscale")
	}
	if c.Width*c.TargetHeight != c.Height*c.TargetWidth {
		return errors.New("target changes source aspect ratio")
	}
	if c.TargetWidth > 1920 || c.TargetHeight > 1080 {
		return errors.New("these H.264 level 4.0 profiles are limited to 1920x1080; a larger source may be downscaled")
	}
	if c.Frames < 2 || c.Frames > 300 || c.FPS < 1 || c.FPS > 30 {
		return errors.New("frames must be 2 to 300 and fps must be 1 to 30")
	}
	return nil
}

func execute(ctx context.Context, c config) error {
	if err := validate(c); err != nil {
		return err
	}
	absOut, err := filepath.Abs(c.Out)
	if err != nil {
		return err
	}
	c.Out = absOut
	entries, err := os.ReadDir(c.Out)
	if err == nil && len(entries) != 0 {
		return fmt.Errorf("output directory is not empty: %s", c.Out)
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(c.Out, 0755); err != nil {
		return err
	}
	r := &runner{ctx: ctx, cfg: c, report: &report{Status: "running", Started: time.Now().UTC().Format(time.RFC3339), Config: c, Profiles: profiles, Streams: map[string]stream{}, FileBytes: map[string]int64{}, PacketStats: map[string]packetStats{}}}
	runErr := r.run()
	if runErr != nil {
		r.report.Status = "failed"
		r.report.Error = runErr.Error()
	} else {
		r.report.Status = "mechanical-checks-passed"
	}
	return errors.Join(runErr, r.writeReport())
}

func (r *runner) command(name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(r.ctx, 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = r.cfg.Out
	start := time.Now()
	output, err := cmd.CombinedOutput()
	logName := fmt.Sprintf("command-%03d.log", len(r.report.Commands)+1)
	record := commandRecord{Args: append([]string{name}, args...), Seconds: time.Since(start).Seconds(), Log: logName}
	if err != nil {
		record.Error = err.Error()
	}
	r.report.Commands = append(r.report.Commands, record)
	if logErr := os.WriteFile(filepath.Join(r.cfg.Out, logName), output, 0644); logErr != nil {
		return output, errors.Join(err, logErr)
	}
	if err != nil {
		return output, fmt.Errorf("%s failed (%s): %w", name, logName, err)
	}
	return output, nil
}

func (r *runner) ffmpeg(args ...string) ([]byte, error) {
	return r.command(r.cfg.FFmpeg, append([]string{"-hide_banner", "-nostdin", "-y", "-xerror", "-err_detect", "explode", "-threads", "2", "-filter_threads", "1", "-filter_complex_threads", "1"}, args...)...)
}

func (r *runner) run() error {
	c := r.cfg
	version, err := r.command(c.FFmpeg, "-version")
	if err != nil {
		return err
	}
	r.report.FFmpegVersion = strings.SplitN(string(version), "\n", 2)[0]
	if err := os.Mkdir(filepath.Join(c.Out, "fixtures"), 0755); err != nil {
		return err
	}
	if err := os.Mkdir(filepath.Join(c.Out, "review"), 0755); err != nil {
		return err
	}
	if err := os.Mkdir(filepath.Join(c.Out, "metrics"), 0755); err != nil {
		return err
	}
	fmt.Println("Generating deterministic RGB frames")
	for i := 0; i < c.Frames; i++ {
		name := filepath.Join(c.Out, "fixtures", fmt.Sprintf("frame-%04d.png", i))
		if err := writePNG(name, chart(c.Width, c.Height, i, c.Frames)); err != nil {
			return err
		}
		data, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		hash := sha256.Sum256(data)
		r.report.FixtureSHA256 = append(r.report.FixtureSHA256, hex.EncodeToString(hash[:]))
	}
	if _, err := r.ffmpeg("-framerate", strconv.Itoa(c.FPS), "-i", "fixtures/frame-%04d.png", "-frames:v", strconv.Itoa(c.Frames), "-vf", "format=bgr0,setsar=1", "-c:v", "ffv1", "-threads", "2", "source-rgb.mkv"); err != nil {
		return err
	}
	scale := fmt.Sprintf("scale=%d:%d:flags=lanczos:in_range=full:out_range=full,format=bgr0,setsar=1", c.TargetWidth, c.TargetHeight)
	if _, err := r.ffmpeg("-i", "source-rgb.mkv", "-vf", scale, "-c:v", "ffv1", "-threads", "2", "target-rgb.mkv"); err != nil {
		return err
	}
	conversion := "scale=iw:ih:flags=lanczos:in_range=full:out_range=limited:out_color_matrix=bt709:out_h_chr_pos=0:out_v_chr_pos=128,format=yuv420p,setsar=1"
	if _, err := r.ffmpeg("-i", "target-rgb.mkv", "-vf", conversion, "-c:v", "ffv1", "-threads", "2", "-pix_fmt", "yuv420p", "-chroma_sample_location", "left", "-color_range", "tv", "-colorspace", "bt709", "-color_primaries", "bt709", "-color_trc", "bt709", "control-420.mkv"); err != nil {
		return err
	}
	if err := r.checkYUVOracle(); err != nil {
		return err
	}
	for _, p := range profiles {
		fmt.Printf("Encoding %s (CRF %d, %s)\n", p.Name, p.CRF, p.Preset)
		args := []string{"-i", "control-420.mkv", "-an", "-c:v", "libx264", "-preset", p.Preset, "-tune", "zerolatency", "-crf", strconv.Itoa(p.CRF), "-maxrate", p.MaxRate, "-bufsize", p.Buffer, "-profile:v", "high", "-level:v", "4.0", "-pix_fmt", "yuv420p", "-bf", "0", "-g", strconv.Itoa(2 * c.FPS), "-threads", "2", "-x264-params", "min-keyint=1:scenecut=40:open-gop=0:repeat-headers=1:aud=1:rc-lookahead=0:sync-lookahead=0", "-chroma_sample_location", "left", "-color_range", "tv", "-colorspace", "bt709", "-color_primaries", "bt709", "-color_trc", "bt709", "-movflags", "+faststart", p.Name + ".mp4"}
		output, err := r.ffmpeg(args...)
		if err != nil {
			return err
		}
		if err := checkEncoderSettings(string(output), p, 2*c.FPS); err != nil {
			return fmt.Errorf("%s: %w", p.Name, err)
		}
		if err := r.checkStream(p.Name+".mp4", true); err != nil {
			return err
		}
		if err := r.checkPackets(p.Name + ".mp4"); err != nil {
			return err
		}
	}
	for _, name := range []string{"source-rgb.mkv", "target-rgb.mkv", "control-420.mkv"} {
		if err := r.checkStream(name, false); err != nil {
			return err
		}
	}
	comparisons := []comparison{
		{Name: "scale", Meaning: "Source RGB versus scaled RGB enlarged back to source size. No H.264 or 4:2:0 in this comparison.", Reference: "source-rgb.mkv", Candidate: "target-rgb.mkv", Width: c.Width, Height: c.Height},
		{Name: "chroma-range", Meaning: "Target RGB versus lossless 4:2:0 round trip. Includes BT.709 limited-range quantization and chroma subsampling, not H.264 loss.", Reference: "target-rgb.mkv", Candidate: "control-420.mkv", Width: c.TargetWidth, Height: c.TargetHeight},
	}
	for _, p := range profiles {
		comparisons = append(comparisons,
			comparison{Name: p.Name + "-codec", Meaning: "Lossless 4:2:0 control versus H.264 decode. Additional encoder/decoder loss only, after the same RGB conversion.", Reference: "control-420.mkv", Candidate: p.Name + ".mp4", Width: c.TargetWidth, Height: c.TargetHeight},
			comparison{Name: p.Name + "-total", Meaning: "Target RGB versus H.264 decode. Includes chroma, range and codec loss, but not the separate scale loss.", Reference: "target-rgb.mkv", Candidate: p.Name + ".mp4", Width: c.TargetWidth, Height: c.TargetHeight})
	}
	for _, comparison := range comparisons {
		fmt.Println("Measuring", comparison.Name)
		measured, err := r.measure(comparison)
		if err != nil {
			return err
		}
		r.report.Comparisons = append(r.report.Comparisons, measured)
	}
	fmt.Println("Mechanical checks passed; image quality still requires baseline and human review.")
	fmt.Println(filepath.Join(c.Out, "index.html"))
	return nil
}

func writePNG(path string, img image.Image) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	err = png.Encode(f, img)
	return errors.Join(err, f.Close())
}

type stream struct {
	Codec          string `json:"codec_name"`
	Width          int    `json:"width"`
	Height         int    `json:"height"`
	PixelFormat    string `json:"pix_fmt"`
	SampleAspect   string `json:"sample_aspect_ratio"`
	DisplayAspect  string `json:"display_aspect_ratio"`
	Range          string `json:"color_range"`
	Space          string `json:"color_space"`
	Primaries      string `json:"color_primaries"`
	Transfer       string `json:"color_transfer"`
	Frames         string `json:"nb_read_frames"`
	Rate           string `json:"avg_frame_rate"`
	Profile        string `json:"profile"`
	Level          int    `json:"level"`
	ChromaLocation string `json:"chroma_location"`
	BFrames        int    `json:"has_b_frames"`
}

func (r *runner) checkStream(name string, h264 bool) error {
	data, err := r.command(r.cfg.FFprobe, "-v", "error", "-threads", "2", "-select_streams", "v:0", "-count_frames", "-show_streams", "-of", "json", name)
	if err != nil {
		return err
	}
	var result struct {
		Streams []stream `json:"streams"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return err
	}
	if len(result.Streams) != 1 {
		return fmt.Errorf("%s: expected exactly one video stream", name)
	}
	s := result.Streams[0]
	r.report.Streams[name] = s
	w, h := r.cfg.TargetWidth, r.cfg.TargetHeight
	if name == "source-rgb.mkv" {
		w, h = r.cfg.Width, r.cfg.Height
	}
	if err := validateStream(s, w, h, r.cfg.Frames, r.cfg.FPS, h264); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	info, err := os.Stat(filepath.Join(r.cfg.Out, name))
	if err != nil {
		return err
	}
	r.report.FileBytes[name] = info.Size()
	return nil
}

func validateStream(s stream, w, h, frames, fps int, h264 bool) error {
	if s.Width != w || s.Height != h {
		return fmt.Errorf("changed dimensions: got %dx%d, expected %dx%d", s.Width, s.Height, w, h)
	}
	if s.SampleAspect != "1:1" {
		return fmt.Errorf("non-square pixels: %q", s.SampleAspect)
	}
	if !rationalEqual(s.DisplayAspect, ":", w, h) {
		return fmt.Errorf("changed display aspect: %q", s.DisplayAspect)
	}
	if s.Frames != strconv.Itoa(frames) {
		return fmt.Errorf("frame count %q, expected %d", s.Frames, frames)
	}
	if !rationalEqual(s.Rate, "/", fps, 1) {
		return fmt.Errorf("frame rate %q, expected %d", s.Rate, fps)
	}
	if h264 && (s.Codec != "h264" || s.PixelFormat != "yuv420p" || s.Range != "tv" || s.Space != "bt709" || s.Primaries != "bt709" || s.Transfer != "bt709" || s.Level != 40 || s.Profile != "High" || s.ChromaLocation != "left" || s.BFrames != 0) {
		return fmt.Errorf("unexpected H.264 pixel format, color metadata or profile/level: %+v", s)
	}
	return nil
}

func rationalEqual(s, separator string, n, d int) bool {
	parts := strings.Split(s, separator)
	if len(parts) != 2 {
		return false
	}
	a, e1 := strconv.Atoi(parts[0])
	b, e2 := strconv.Atoi(parts[1])
	return e1 == nil && e2 == nil && a > 0 && b > 0 && a*d == b*n
}

func readFrame(reader io.Reader, size int) ([]byte, error) {
	b := make([]byte, size)
	_, err := io.ReadFull(reader, b)
	return b, err
}
