package airplay

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

type screenCanvas struct {
	width, height             int
	activeWidth, activeHeight int
	left, top                 int
}

var ErrHiddenCursorUnsupported = errors.New("Windows screen capture cannot guarantee cursor exclusion; enable cursor sharing")

func validateWindowsCaptureCursor(showCursor, synthetic bool) error {
	if !showCursor && !synthetic {
		return ErrHiddenCursorUnsupported
	}
	return nil
}

func windowsScreenCanvas(sourceWidth, sourceHeight, receiverWidth, receiverHeight int) (screenCanvas, error) {
	if sourceWidth < 2 || sourceHeight < 2 || sourceWidth > 16384 || sourceHeight > 16384 {
		return screenCanvas{}, fmt.Errorf("invalid capture pixel size %dx%d", sourceWidth, sourceHeight)
	}
	if receiverWidth < 0 || receiverHeight < 0 || receiverWidth > 16384 || receiverHeight > 16384 || (receiverWidth == 0) != (receiverHeight == 0) {
		return screenCanvas{}, fmt.Errorf("invalid receiver canvas %dx%d", receiverWidth, receiverHeight)
	}
	width, height := (sourceWidth+1)&^1, (sourceHeight+1)&^1
	if receiverWidth != 0 {
		width, height = receiverWidth&^1, receiverHeight&^1
	}
	if width > 1920 || height > 1080 {
		if int64(width)*1080 > int64(height)*1920 {
			height = int(int64(height)*1920/int64(width)) &^ 1
			width = 1920
		} else {
			width = int(int64(width)*1080/int64(height)) &^ 1
			height = 1080
		}
	}
	if width < 2 || height < 2 {
		return screenCanvas{}, fmt.Errorf("receiver canvas %dx%d is too small for H.264 4:2:0", receiverWidth, receiverHeight)
	}
	activeWidth, activeHeight := sourceWidth, sourceHeight
	if activeWidth > width || activeHeight > height {
		if int64(sourceWidth)*int64(height) > int64(sourceHeight)*int64(width) {
			activeWidth = width
			activeHeight = int(int64(sourceHeight)*int64(width)/int64(sourceWidth)) &^ 1
		} else {
			activeHeight = height
			activeWidth = int(int64(sourceWidth)*int64(height)/int64(sourceHeight)) &^ 1
		}
	}
	if activeWidth < 2 || activeHeight < 2 {
		return screenCanvas{}, fmt.Errorf("capture aspect ratio cannot fit receiver canvas %dx%d", width, height)
	}
	return screenCanvas{
		width: width, height: height, activeWidth: activeWidth, activeHeight: activeHeight,
		left: ((width - activeWidth) / 2) &^ 1, top: ((height - activeHeight) / 2) &^ 1,
	}, nil
}

func windowsFFmpegVideoArgs(cfg CaptureConfig, sourceWidth, sourceHeight int) ([]string, screenCanvas, error) {
	if err := ValidateCaptureQuality(cfg.Quality); err != nil {
		return nil, screenCanvas{}, err
	}
	if normalizeVideoCodec(cfg.VideoCodec) != VideoCodecH264 {
		return nil, screenCanvas{}, fmt.Errorf("Windows capture currently supports SDR H.264 only")
	}
	if cfg.HWAccel != "" && cfg.HWAccel != "auto" && cfg.HWAccel != "none" && cfg.HWAccel != "nvenc" {
		return nil, screenCanvas{}, fmt.Errorf("Windows capture encoder %q is unavailable; use auto, none, or nvenc", cfg.HWAccel)
	}
	fps := cfg.FPS
	if fps == 0 {
		fps = 30
	}
	if fps < 1 || fps > 30 {
		return nil, screenCanvas{}, fmt.Errorf("initial Windows capture supports 1..30 fps; received %d", fps)
	}
	canvas, err := windowsScreenCanvas(sourceWidth, sourceHeight, cfg.MaxWidth, cfg.MaxHeight)
	if err != nil {
		return nil, screenCanvas{}, err
	}
	cfg.MaxWidth, cfg.MaxHeight = canvas.width, canvas.height
	profile := captureQualityProfile(cfg)
	if profile.maxBitrateKbps < 100 || profile.maxBitrateKbps > 20000 {
		return nil, screenCanvas{}, fmt.Errorf("Windows H.264 bitrate must be 100..20000 kbit/s")
	}
	filter := "lanczos"
	if cfg.Quality == "balanced" {
		filter = "bicubic"
	}
	stages := []string{"format=gbrp16le"}
	if canvas.activeWidth != sourceWidth || canvas.activeHeight != sourceHeight {
		stages = append(stages, fmt.Sprintf("zscale=w=%d:h=%d:filter=%s:matrixin=gbr:matrix=gbr:primariesin=bt709:primaries=bt709:transferin=iec61966-2-1:transfer=iec61966-2-1:rangein=full:range=full", canvas.activeWidth, canvas.activeHeight, filter))
	}
	if canvas.width != canvas.activeWidth || canvas.height != canvas.activeHeight {
		stages = append(stages, fmt.Sprintf("pad=%d:%d:%d:%d:color=black", canvas.width, canvas.height, canvas.left, canvas.top))
	}
	// zimg's default BT.709 transfer conversion uses a display-referred BT.1886
	// curve. Apply the specified sRGB EOTF and BT.709 OETF explicitly at 16 bits.
	transfer := "'st(0,val/maxval);st(1,if(lte(ld(0),0.04045),ld(0)/12.92,pow((ld(0)+0.055)/1.055,2.4)));maxval*if(lt(ld(1),0.018),4.5*ld(1),1.099*pow(ld(1),0.45)-0.099)'"
	stages = append(stages,
		"lutrgb=r="+transfer+":g="+transfer+":b="+transfer,
		"zscale=filter="+filter+":matrixin=gbr:primariesin=bt709:transferin=bt709:rangein=full:matrix=bt709:primaries=bt709:transfer=bt709:range=limited:chromal=left:dither=ordered",
		"format=yuv420p", "setsar=1")
	args := []string{
		"-hide_banner", "-nostdin", "-loglevel", "warning",
		"-probesize", "32", "-analyzeduration", "0", "-fpsprobesize", "0",
		"-f", "rawvideo", "-pixel_format", "bgra", "-video_size", fmt.Sprintf("%dx%d", sourceWidth, sourceHeight),
		"-framerate", strconv.Itoa(fps), "-i", "pipe:0", "-an", "-filter_threads", "1",
	}
	if cfg.HWAccel == "nvenc" {
		stages = append(stages, "format=nv12")
	}
	args = append(args, "-vf", strings.Join(stages, ","))
	level := "4.0"
	if canvas.width <= 1280 && canvas.height <= 720 && profile.maxBitrateKbps <= 14000 {
		level = "3.1"
	}
	if cfg.HWAccel == "nvenc" {
		args = append(args, "-c:v", "h264_nvenc", "-preset", "p5", "-tune", "ll", "-rc", "vbr",
			"-b:v", fmt.Sprintf("%dk", profile.maxBitrateKbps*3/4),
			"-rc-lookahead", "0", "-zerolatency", "1", "-delay", "0", "-aud", "1")
	} else {
		args = append(args, "-c:v", "libx264", "-preset", profile.preset, "-tune", "zerolatency",
			"-crf", strconv.Itoa(profile.crf), "-threads", strconv.Itoa(profile.threads),
			"-x264-params", "open-gop=0:repeat-headers=1:aud=1:rc-lookahead=0:sync-lookahead=0")
	}
	args = append(args,
		"-maxrate", fmt.Sprintf("%dk", profile.maxBitrateKbps),
		"-bufsize", fmt.Sprintf("%dk", profile.maxBitrateKbps*profile.vbvMilliseconds/1000),
		"-g", strconv.Itoa(keyframeIntervalFrames(fps)), "-bf", "0", "-profile:v", "high", "-level:v", level, "-tag:v", "H264",
		"-color_range", "tv", "-colorspace", "bt709", "-color_primaries", "bt709", "-color_trc", "bt709",
		"-chroma_sample_location", "left", "-fps_mode", "passthrough", "-flush_packets", "1", "-f", "avi", "pipe:1")
	return args, canvas, nil
}

type aviScreenReader struct {
	reader     io.Reader
	ctx        context.Context
	timestamps <-chan time.Time
	started    bool
	ended      bool
}

const maximumCaptureAVIHeaderBytes = 64 << 10

func (r *aviScreenReader) start() error {
	var header [12]byte
	if _, err := io.ReadFull(r.reader, header[:]); err != nil {
		return fmt.Errorf("FFmpeg AVI header: %w", err)
	}
	if string(header[:4]) != "RIFF" || string(header[8:]) != "AVI " {
		return fmt.Errorf("FFmpeg output is not the expected streaming AVI")
	}
	consumed, videoStreams := len(header), 0
	for consumed+12 <= maximumCaptureAVIHeaderBytes {
		tag, size, err := r.readChunkHeader()
		if err != nil {
			return fmt.Errorf("FFmpeg AVI stream header: %w", err)
		}
		consumed += 8
		if tag == "LIST" {
			if size < 4 {
				return fmt.Errorf("FFmpeg AVI list is missing its type")
			}
			var kind [4]byte
			if _, err := io.ReadFull(r.reader, kind[:]); err != nil {
				return fmt.Errorf("FFmpeg AVI list type: %w", err)
			}
			consumed += 4
			switch string(kind[:]) {
			case "movi":
				if videoStreams != 1 {
					return fmt.Errorf("FFmpeg AVI must contain one H.264 video stream")
				}
				r.started = true
				return nil
			case "hdrl", "strl", "odml":
				continue
			}
			size -= 4
		}
		padded := int64(size) + int64(size&1)
		if padded > int64(maximumCaptureAVIHeaderBytes-consumed) {
			return fmt.Errorf("FFmpeg AVI header exceeds %d bytes", maximumCaptureAVIHeaderBytes)
		}
		if tag == "strh" {
			if size < 8 {
				return fmt.Errorf("FFmpeg AVI video stream header is truncated")
			}
			var stream [8]byte
			if _, err := io.ReadFull(r.reader, stream[:]); err != nil {
				return fmt.Errorf("FFmpeg AVI video stream header: %w", err)
			}
			if string(stream[:4]) != "vids" || string(stream[4:]) != "H264" {
				return fmt.Errorf("FFmpeg AVI contains an unexpected stream codec")
			}
			videoStreams++
			padded -= int64(len(stream))
			consumed += len(stream)
		}
		if _, err := io.CopyN(io.Discard, r.reader, padded); err != nil {
			return fmt.Errorf("FFmpeg AVI header chunk: %w", err)
		}
		consumed += int(padded)
	}
	return fmt.Errorf("FFmpeg AVI header exceeds %d bytes", maximumCaptureAVIHeaderBytes)
}

func (r *aviScreenReader) readChunkHeader() (string, uint32, error) {
	var header [8]byte
	if _, err := io.ReadFull(r.reader, header[:]); err != nil {
		return "", 0, err
	}
	return string(header[:4]), binary.LittleEndian.Uint32(header[4:]), nil
}

func (r *aviScreenReader) ReadVideoAccessUnit() (VideoAccessUnit, error) {
	if r.ended {
		return VideoAccessUnit{}, io.EOF
	}
	if !r.started {
		if err := r.start(); err != nil {
			return VideoAccessUnit{}, err
		}
	}
	tag, size, err := r.readChunkHeader()
	if err != nil {
		if err == io.EOF {
			r.ended = true
		}
		return VideoAccessUnit{}, err
	}
	if tag == "idx1" {
		r.ended = true
		return VideoAccessUnit{}, io.EOF
	}
	if tag != "00dc" {
		return VideoAccessUnit{}, fmt.Errorf("unexpected FFmpeg AVI movie chunk %q", tag)
	}
	if size == 0 || size > maxVideoAccessUnitBytes {
		return VideoAccessUnit{}, fmt.Errorf("FFmpeg AVI access unit size %d is outside 1..%d bytes", size, maxVideoAccessUnitBytes)
	}
	data := make([]byte, int(size))
	// AVI is only the local packet envelope. One 00dc chunk contains one AU,
	// which can be released without reading the next capture's AUD or packet.
	if _, err := io.ReadFull(r.reader, data); err != nil {
		return VideoAccessUnit{}, fmt.Errorf("FFmpeg AVI access unit: %w", err)
	}
	if size&1 != 0 {
		var padding [1]byte
		if _, err := io.ReadFull(r.reader, padding[:]); err != nil {
			return VideoAccessUnit{}, fmt.Errorf("FFmpeg AVI access unit padding: %w", err)
		}
	}
	return r.accessUnit(data)
}

func (r *aviScreenReader) accessUnit(data []byte) (VideoAccessUnit, error) {
	if len(data) > maxVideoAccessUnitBytes {
		return VideoAccessUnit{}, fmt.Errorf("FFmpeg access unit exceeds %d bytes", maxVideoAccessUnitBytes)
	}
	haveVCL := false
	for _, nal := range splitAnnexBAccessUnit(data) {
		raw := stripStartCode(nal)
		if len(raw) > 0 && raw[0]&0x1f >= 1 && raw[0]&0x1f <= 5 {
			haveVCL = true
		}
	}
	if !haveVCL {
		return VideoAccessUnit{}, fmt.Errorf("FFmpeg access unit contains no picture")
	}
	// With no B frames and passthrough cadence each encoded AU consumes exactly
	// one submitted raw frame. The side channel retains its actual capture time.
	select {
	case pts, ok := <-r.timestamps:
		if !ok || pts.IsZero() {
			return VideoAccessUnit{}, fmt.Errorf("FFmpeg access unit has no matching capture timestamp")
		}
		return VideoAccessUnit{AnnexB: data, PTS: pts}, nil
	case <-r.ctx.Done():
		return VideoAccessUnit{}, r.ctx.Err()
	}
}
