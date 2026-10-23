package airplay

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os/exec"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestCaptureQualityProfiles(t *testing.T) {
	for _, value := range []string{"", "quality", "balanced"} {
		if err := ValidateCaptureQuality(value); err != nil {
			t.Fatal(err)
		}
	}
	for _, value := range []string{"high", "QUALITY", " quality"} {
		if ValidateCaptureQuality(value) == nil {
			t.Fatalf("accepted quality %q", value)
		}
	}
	quality := captureQualityProfile(CaptureConfig{MaxWidth: 1920, MaxHeight: 1080})
	if quality.preset != "faster" || quality.crf != 18 || quality.maxBitrateKbps != 12000 || quality.vbvMilliseconds != 150 {
		t.Fatalf("default profile = %+v", quality)
	}
	if explicit := captureQualityProfile(CaptureConfig{Quality: "quality", MaxWidth: 1920, MaxHeight: 1080}); explicit != quality {
		t.Fatalf("zero quality profile differs from explicit quality: %+v != %+v", quality, explicit)
	}
	balanced := captureQualityProfile(CaptureConfig{Quality: "balanced", MaxWidth: 1280, MaxHeight: 720})
	if balanced.preset != "veryfast" || balanced.crf != 20 || balanced.maxBitrateKbps != 4000 || balanced.vbvMilliseconds != 100 {
		t.Fatalf("balanced 720p profile = %+v", balanced)
	}
	if explicit := captureQualityProfile(CaptureConfig{Bitrate: 2500}); explicit.maxBitrateKbps != 2500 {
		t.Fatalf("explicit bitrate ignored: %+v", explicit)
	}
	if quality.threads < 1 || quality.threads > 6 || quality.threads > max(1, runtime.NumCPU()-1) {
		t.Fatalf("invalid thread bound: %+v", quality)
	}
}

func TestGStreamerQualityVBVUsesMilliseconds(t *testing.T) {
	for _, test := range []struct {
		quality string
		want    []string
	}{
		{want: []string{"speed-preset=faster", "pass=qual", "quantizer=18", "bitrate=12000", "vbv-buf-capacity=150"}},
		{quality: "balanced", want: []string{"speed-preset=veryfast", "pass=qual", "quantizer=20", "bitrate=8000", "vbv-buf-capacity=100"}},
	} {
		encoder, err := selectGstEncoderWithProbe(CaptureConfig{HWAccel: "none", Quality: test.quality, FPS: 30, MaxWidth: 1920, MaxHeight: 1080}, func(name string) bool { return name == "x264enc" }, false)
		if err != nil {
			t.Fatal(err)
		}
		joined := strings.Join(encoder.parts, " ")
		for _, want := range test.want {
			if !strings.Contains(joined, want) {
				t.Fatalf("missing %q in %s", want, joined)
			}
		}
		if strings.Contains(joined, "vbv-maxrate") || strings.Contains(joined, "superfast") {
			t.Fatalf("retained obsolete rate-control settings: %s", joined)
		}
	}
}

func TestAutoGStreamerQualityPrefersCappedCRFSoftware(t *testing.T) {
	var probes []string
	encoder, err := selectGstEncoderWithProbe(CaptureConfig{Quality: "quality"}, func(name string) bool {
		probes = append(probes, name)
		return name == "x264enc" || name == "openh264enc"
	}, false)
	if err != nil || encoder.parts[0] != "x264enc" {
		t.Fatalf("quality software selection = %+v, %v", encoder, err)
	}
	if !reflect.DeepEqual(probes, []string{"vulkanh264enc", "nvh264enc", "vah264enc", "x264enc"}) {
		t.Fatalf("quality encoder probe order = %v", probes)
	}
}

func TestWindowsScreenCanvas(t *testing.T) {
	for _, test := range []struct {
		name           string
		sw, sh, rw, rh int
		want           screenCanvas
	}{
		{name: "native", sw: 1920, sh: 1080, rw: 1920, rh: 1080, want: screenCanvas{width: 1920, height: 1080, activeWidth: 1920, activeHeight: 1080}},
		{name: "16:10", sw: 2560, sh: 1600, rw: 1920, rh: 1080, want: screenCanvas{width: 1920, height: 1080, activeWidth: 1728, activeHeight: 1080, left: 96}},
		{name: "fractional DPI", sw: 3024, sh: 1964, rw: 1280, rh: 720, want: screenCanvas{width: 1280, height: 720, activeWidth: 1108, activeHeight: 720, left: 86}},
		{name: "no upscale", sw: 1280, sh: 720, rw: 1920, rh: 1080, want: screenCanvas{width: 1920, height: 1080, activeWidth: 1280, activeHeight: 720, left: 320, top: 180}},
		{name: "odd source preserves edges", sw: 501, sh: 301, want: screenCanvas{width: 502, height: 302, activeWidth: 501, activeHeight: 301}},
		{name: "odd receiver", sw: 1920, sh: 1080, rw: 1279, rh: 719, want: screenCanvas{width: 1278, height: 718, activeWidth: 1276, activeHeight: 718}},
		{name: "4K receiver is not forced", sw: 3840, sh: 2160, rw: 3840, rh: 2160, want: screenCanvas{width: 1920, height: 1080, activeWidth: 1920, activeHeight: 1080}},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := windowsScreenCanvas(test.sw, test.sh, test.rw, test.rh)
			if err != nil || got != test.want {
				t.Fatalf("canvas = %+v, %v; want %+v", got, err, test.want)
			}
		})
	}
	for _, size := range [][4]int{{0, 1080, 1920, 1080}, {1920, 1080, 0, 720}, {1920, 1080, 1, 1}, {1920, 1080, -1, 720}, {1920, 1080, 1 << 30, 720}, {16384, 2, 2, 1080}} {
		if got, err := windowsScreenCanvas(size[0], size[1], size[2], size[3]); err == nil {
			t.Fatalf("accepted invalid geometry %v: %+v", size, got)
		}
	}
}

func TestWindowsFFmpegColorScalingAndVBVArguments(t *testing.T) {
	cfg := CaptureConfig{FPS: 30, HWAccel: "none", MaxWidth: 1920, MaxHeight: 1080}
	args, _, err := windowsFFmpegVideoArgs(cfg, 2560, 1600)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{"-probesize 32", "-analyzeduration 0", "-fpsprobesize 0", "-pixel_format bgra", "-video_size 2560x1600", "zscale=w=1728:h=1080:filter=lanczos", "pad=1920:1080:96:0:color=black", "transferin=iec61966-2-1", "matrix=bt709", "range=limited", "format=yuv420p", "-crf 18", "-maxrate 12000k", "-bufsize 1800k", "-bf 0", "-level:v 4.0", "-tag:v H264", "-fps_mode passthrough", "-flush_packets 1 -f avi pipe:1"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in %s", want, joined)
		}
	}
	if strings.Index(joined, "zscale=w=") > strings.Index(joined, "format=yuv420p") {
		t.Fatal("RGB is subsampled before resizing")
	}
	cfg.HWAccel = "nvenc"
	args, _, err = windowsFFmpegVideoArgs(cfg, 2560, 1600)
	if err != nil {
		t.Fatal(err)
	}
	joined = strings.Join(args, " ")
	if strings.Contains(joined, "-cq") || !strings.Contains(joined, "format=yuv420p,setsar=1,format=nv12") || !strings.Contains(joined, "-rc vbr") {
		t.Fatalf("NVENC must use explicit YUV conversion and bounded VBR without CQ: %s", joined)
	}
	for _, cfg := range []CaptureConfig{{Quality: "unknown"}, {VideoCodec: VideoCodecHEVC}, {HWAccel: "vaapi"}, {FPS: 60}, {FPS: -1}, {Bitrate: 20001}} {
		if _, _, err := windowsFFmpegVideoArgs(cfg, 1920, 1080); err == nil {
			t.Fatalf("accepted unsupported Windows capture config %+v", cfg)
		}
	}
}

type shortCaptureReader struct {
	io.Reader
	size int
}

func (r shortCaptureReader) Read(buffer []byte) (int, error) {
	return r.Reader.Read(buffer[:min(len(buffer), r.size)])
}

func TestFFmpegAVIReaderPreservesSourceTimestampsAndSlices(t *testing.T) {
	units := [][]byte{
		captureTestAnnexB([]byte{9, 0xf0}, []byte{0x67, 1}, []byte{0x68, 2}, []byte{0x65, 0x80, 3}, []byte{0x65, 0x40, 4}),
		captureTestAnnexB([]byte{9, 0xf0}, []byte{0x61, 0x80, 5}),
		captureTestAnnexB([]byte{9, 0xf0}, []byte{0x61, 0x80, 6}),
	}
	stream := captureTestAVI(units...)
	base := time.Now()
	for _, chunkSize := range []int{1, 2, 3, 4, 5, 17, len(stream)} {
		timestamps := make(chan time.Time, len(units))
		wantTimes := []time.Time{base, base.Add(41 * time.Millisecond), base.Add(123 * time.Millisecond)}
		for _, pts := range wantTimes {
			timestamps <- pts
		}
		close(timestamps)
		reader := &aviScreenReader{reader: shortCaptureReader{Reader: bytes.NewReader(stream), size: chunkSize}, ctx: context.Background(), timestamps: timestamps}
		for index, want := range units {
			got, err := reader.ReadVideoAccessUnit()
			if err != nil || !bytes.Equal(got.AnnexB, want) || got.PTS != wantTimes[index] {
				t.Fatalf("chunk %d AU %d = {%x %v}, %v; want {%x %v}", chunkSize, index, got.AnnexB, got.PTS, err, want, wantTimes[index])
			}
		}
		if _, err := reader.ReadVideoAccessUnit(); err != io.EOF {
			t.Fatalf("chunk %d final error = %v", chunkSize, err)
		}
	}
}

func TestFFmpegAVIReaderRejectsMissingPTSAndUnboundedPacket(t *testing.T) {
	stream := captureTestAVI(captureTestAnnexB([]byte{9, 0xf0}, []byte{0x65, 0x80}))
	timestamps := make(chan time.Time)
	close(timestamps)
	reader := &aviScreenReader{reader: bytes.NewReader(stream), ctx: context.Background(), timestamps: timestamps}
	if _, err := reader.ReadVideoAccessUnit(); err == nil || !strings.Contains(err.Error(), "no matching capture timestamp") {
		t.Fatalf("missing timestamp error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	reader = &aviScreenReader{reader: bytes.NewReader(stream), ctx: ctx, timestamps: make(chan time.Time)}
	if _, err := reader.ReadVideoAccessUnit(); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled timestamp wait = %v", err)
	}
	oversized := append(captureTestAVI(), []byte("00dc\x00\x00\x00\x00")...)
	binary.LittleEndian.PutUint32(oversized[len(oversized)-4:], maxVideoAccessUnitBytes+1)
	reader = &aviScreenReader{reader: bytes.NewReader(oversized), ctx: context.Background(), timestamps: timestamps}
	if _, err := reader.ReadVideoAccessUnit(); err == nil || !strings.Contains(err.Error(), "outside") {
		t.Fatalf("unbounded packet error = %v", err)
	}
}

func captureTestRIFFChunk(tag string, data []byte) []byte {
	chunk := append([]byte(tag), 0, 0, 0, 0)
	binary.LittleEndian.PutUint32(chunk[4:], uint32(len(data)))
	chunk = append(chunk, data...)
	if len(data)&1 != 0 {
		chunk = append(chunk, 0)
	}
	return chunk
}

func captureTestAVI(units ...[]byte) []byte {
	streamHeader := append([]byte("vidsH264"), make([]byte, 48)...)
	streamList := captureTestRIFFChunk("LIST", append([]byte("strl"), captureTestRIFFChunk("strh", streamHeader)...))
	headerList := captureTestRIFFChunk("LIST", append([]byte("hdrl"), streamList...))
	stream := append([]byte("RIFF\xff\xff\xff\xffAVI "), headerList...)
	stream = append(stream, []byte("LIST\xff\xff\xff\xffmovi")...)
	for _, unit := range units {
		stream = append(stream, captureTestRIFFChunk("00dc", unit)...)
	}
	return stream
}

func TestFFmpegAVIReaderRejectsMalformedEnvelope(t *testing.T) {
	unit := captureTestAnnexB([]byte{9, 0xf0}, []byte{0x65, 0x80, 1})
	packet := captureTestAVI(unit)
	oversizedHeader := append([]byte("RIFF\xff\xff\xff\xffAVI JUNK"), 0, 0, 0, 0)
	binary.LittleEndian.PutUint32(oversizedHeader[len(oversizedHeader)-4:], maximumCaptureAVIHeaderBytes)
	for _, stream := range [][]byte{
		[]byte("RIFF\xff\xff\xff\xffNUT "),
		bytes.Replace(packet, []byte("vidsH264"), []byte("audsH264"), 1),
		bytes.Replace(packet, []byte("vidsH264"), []byte("vidsHEVC"), 1),
		oversizedHeader,
		packet[:len(packet)-1],
		append(captureTestAVI(), captureTestRIFFChunk("01wb", []byte{1, 2})...),
		captureTestAVI(captureTestAnnexB([]byte{0x67, 1}, []byte{0x68, 2})),
	} {
		timestamps := make(chan time.Time, 1)
		timestamps <- time.Now()
		reader := &aviScreenReader{reader: bytes.NewReader(stream), ctx: context.Background(), timestamps: timestamps}
		if _, err := reader.ReadVideoAccessUnit(); err == nil {
			t.Fatalf("accepted malformed AVI envelope: %x", stream[:min(len(stream), 32)])
		}
	}
}

func TestFFmpegAVIReaderReturnsPacketBeforeNextPacketOrEOF(t *testing.T) {
	input, output := io.Pipe()
	t.Cleanup(func() { _ = input.Close(); _ = output.Close() })
	unit := captureTestAnnexB([]byte{9, 0xf0}, []byte{0x67, 1}, []byte{0x68, 2}, []byte{0x65, 0x80})
	pts := time.Now()
	timestamps := make(chan time.Time, 1)
	timestamps <- pts
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	reader := &aviScreenReader{reader: input, ctx: ctx, timestamps: timestamps}
	fixture := &liveWindowsEncoderFixture{ctx: ctx, frames: reader, stderr: newCaptureStderrTail()}
	written := make(chan error, 1)
	go func() {
		_, err := output.Write(captureTestAVI(unit))
		written <- err
	}()
	got := fixture.read(t, 500*time.Millisecond)
	if got.PTS != pts || !bytes.Equal(got.AnnexB, unit) {
		t.Fatalf("immediate AVI unit = {%x %v}, want {%x %v}", got.AnnexB, got.PTS, unit, pts)
	}
	if err := <-written; err != nil {
		t.Fatal(err)
	}
}

func TestFFmpegWindowsPipelineSyntheticDecode(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux synthetic codec validation; not a Windows native-capture test")
	}
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("FFmpeg is unavailable")
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe is unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	const width, height, frames = 320, 180, 6
	colors := [][3]byte{{0, 0, 0}, {255, 255, 255}, {255, 0, 0}, {0, 255, 0}, {0, 0, 255}, {128, 128, 128}}
	pixels := make([]byte, width*height*4)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			color := colors[x*len(colors)/width]
			offset := (y*width + x) * 4
			pixels[offset], pixels[offset+1], pixels[offset+2], pixels[offset+3] = color[2], color[1], color[0], 255
		}
	}
	args, _, err := windowsFFmpegVideoArgs(CaptureConfig{HWAccel: "none", FPS: 30, MaxWidth: width, MaxHeight: height}, width, height)
	if err != nil {
		t.Fatal(err)
	}
	encode := exec.CommandContext(ctx, ffmpeg, args...)
	encode.Stdin = bytes.NewReader(bytes.Repeat(pixels, frames))
	var stderr bytes.Buffer
	encode.Stderr = &stderr
	encoded, err := encode.Output()
	if err != nil {
		t.Fatalf("synthetic Windows encoding graph: %v\n%s", err, stderr.String())
	}
	decode := exec.CommandContext(ctx, ffmpeg, "-v", "error", "-xerror", "-f", "avi", "-i", "pipe:0", "-pix_fmt", "yuv420p", "-f", "rawvideo", "pipe:1")
	decode.Stdin = bytes.NewReader(encoded)
	stderr.Reset()
	decode.Stderr = &stderr
	decoded, err := decode.Output()
	if err != nil {
		t.Fatalf("decode synthetic output: %v\n%s", err, stderr.String())
	}
	frameSize := width * height * 3 / 2
	if len(decoded) != frameSize*frames {
		t.Fatalf("decoded %d bytes, want %d complete YUV frames", len(decoded), frames)
	}
	for frame := 0; frame < frames; frame++ {
		for patch, rgb := range colors {
			x, y := (patch*2+1)*width/(2*len(colors)), height/2
			luma := float64(decoded[frame*frameSize+y*width+x])
			u := float64(decoded[frame*frameSize+width*height+(y/2)*(width/2)+x/2])
			v := float64(decoded[frame*frameSize+width*height*5/4+(y/2)*(width/2)+x/2])
			wantY, wantU, wantV := capturePatch709Oracle(rgb)
			if math.Abs(luma-wantY) > 2 || math.Abs(u-wantU) > 2 || math.Abs(v-wantV) > 2 {
				t.Fatalf("frame %d RGB %v decoded YUV %.0f/%.0f/%.0f, want %.0f/%.0f/%.0f within 2 codes", frame, rgb, luma, u, v, wantY, wantU, wantV)
			}
		}
	}
	probe := exec.CommandContext(ctx, ffprobe, "-v", "error", "-f", "avi", "-i", "pipe:0", "-select_streams", "v:0", "-show_entries", "stream=profile,level,width,height,has_b_frames,pix_fmt,color_range,color_space,color_transfer,color_primaries,sample_aspect_ratio", "-of", "json")
	probe.Stdin = bytes.NewReader(encoded)
	metadata, err := probe.Output()
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Streams []map[string]any `json:"streams"`
	}
	if err := json.Unmarshal(metadata, &result); err != nil || len(result.Streams) != 1 {
		t.Fatalf("ffprobe metadata = %s, %v", metadata, err)
	}
	for key, want := range map[string]any{"profile": "High", "level": float64(31), "width": float64(width), "height": float64(height), "has_b_frames": float64(0), "pix_fmt": "yuv420p", "color_range": "tv", "color_space": "bt709", "color_transfer": "bt709", "color_primaries": "bt709", "sample_aspect_ratio": "1:1"} {
		if !reflect.DeepEqual(result.Streams[0][key], want) {
			t.Fatalf("ffprobe %s = %v, want %v\n%s", key, result.Streams[0][key], want, metadata)
		}
	}
	timestamps := make(chan time.Time, frames)
	base := time.Now()
	for frame := 0; frame < frames; frame++ {
		timestamps <- base.Add(time.Duration(frame*41) * time.Millisecond)
	}
	close(timestamps)
	reader := &aviScreenReader{reader: bytes.NewReader(encoded), ctx: ctx, timestamps: timestamps}
	for frame := 0; frame < frames; frame++ {
		unit, err := reader.ReadVideoAccessUnit()
		if err != nil || unit.PTS != base.Add(time.Duration(frame*41)*time.Millisecond) {
			t.Fatalf("encoded AU %d timestamp = %v, %v", frame, unit.PTS, err)
		}
	}
	if _, err := reader.ReadVideoAccessUnit(); err != io.EOF {
		t.Fatalf("encoded AU EOF = %v", err)
	}
	t.Logf("decoded %d synthetic %dx%d frames, checked BT.709 patch interiors and SPS/VUI metadata; native Windows capture and physical receiver remain unverified", frames, width, height)
}

func capturePatch709Oracle(rgb [3]byte) (float64, float64, float64) {
	var values [3]float64
	for index, value := range rgb {
		srgb := float64(value) / 255
		linear := srgb / 12.92
		if srgb > 0.04045 {
			linear = math.Pow((srgb+0.055)/1.055, 2.4)
		}
		values[index] = linear * 4.5
		if linear >= 0.018 {
			values[index] = 1.099*math.Pow(linear, 0.45) - 0.099
		}
	}
	y := .2126*values[0] + .7152*values[1] + .0722*values[2]
	return math.Round(16 + 219*y), math.Round(128 + 224*(values[2]-y)/1.8556), math.Round(128 + 224*(values[0]-y)/1.5748)
}

func TestFFmpegWindowsPipelineDecodedGeometry(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux synthetic decode test; not a Windows desktop-capture test")
	}
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("FFmpeg is unavailable")
	}
	for _, test := range []struct {
		name           string
		sw, sh, cw, ch int
	}{
		{name: "odd native edges without upscale", sw: 61, sh: 37, cw: 128, ch: 72},
		{name: "RGB downscale and pillarbox", sw: 80, sh: 50, cw: 64, ch: 36},
		{name: "portrait aspect padding", sw: 50, sh: 80, cw: 64, ch: 36},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			args, canvas, err := windowsFFmpegVideoArgs(CaptureConfig{HWAccel: "none", FPS: 30, MaxWidth: test.cw, MaxHeight: test.ch}, test.sw, test.sh)
			if err != nil {
				t.Fatal(err)
			}
			encode := exec.CommandContext(ctx, ffmpeg, args...)
			encode.Stdin = bytes.NewReader(bytes.Repeat([]byte{255, 255, 255, 255}, test.sw*test.sh*2))
			var stderr bytes.Buffer
			encode.Stderr = &stderr
			encoded, err := encode.Output()
			if err != nil {
				t.Fatalf("encode geometry fixture: %v\n%s", err, stderr.String())
			}
			decode := exec.CommandContext(ctx, ffmpeg, "-v", "error", "-xerror", "-f", "avi", "-i", "pipe:0", "-pix_fmt", "yuv420p", "-f", "rawvideo", "pipe:1")
			decode.Stdin = bytes.NewReader(encoded)
			stderr.Reset()
			decode.Stderr = &stderr
			decoded, err := decode.Output()
			if err != nil {
				t.Fatalf("decode geometry fixture: %v\n%s", err, stderr.String())
			}
			frameSize := canvas.width * canvas.height * 3 / 2
			if len(decoded) != frameSize*2 {
				t.Fatalf("geometry decoded %d bytes, want %d", len(decoded), frameSize*2)
			}
			for frame := 0; frame < 2; frame++ {
				for y := 0; y < canvas.height; y++ {
					for x := 0; x < canvas.width; x++ {
						want := 16
						if x >= canvas.left && x < canvas.left+canvas.activeWidth && y >= canvas.top && y < canvas.top+canvas.activeHeight {
							want = 235
						}
						got := int(decoded[frame*frameSize+y*canvas.width+x])
						if (got > 125) != (want == 235) {
							t.Fatalf("frame %d pixel %d,%d luma %d is on the wrong side of the active/padding boundary; want %d, rectangle %+v", frame, x, y, got, want, canvas)
						}
						nearBoundary := absCaptureInt(x-canvas.left) < 8 || absCaptureInt(x-(canvas.left+canvas.activeWidth)) < 8 || absCaptureInt(y-canvas.top) < 8 || absCaptureInt(y-(canvas.top+canvas.activeHeight)) < 8
						if !nearBoundary && (got < want-2 || got > want+2) {
							t.Fatalf("frame %d interior pixel %d,%d luma %d, want %d within 2 codes", frame, x, y, got, want)
						}
					}
				}
			}
		})
	}
}

func absCaptureInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
