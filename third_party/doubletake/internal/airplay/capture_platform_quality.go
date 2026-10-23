package airplay

import (
	"fmt"
	"runtime"
)

type screenQualityProfile struct {
	preset          string
	crf             int
	maxBitrateKbps  int
	vbvMilliseconds int
	threads         int
}

func ValidateCaptureQuality(quality string) error {
	switch quality {
	case "", "quality", "balanced":
		return nil
	default:
		return fmt.Errorf("unknown capture quality %q (want quality or balanced)", quality)
	}
}

func captureQualityProfile(cfg CaptureConfig) screenQualityProfile {
	profile := screenQualityProfile{preset: "faster", crf: 18, maxBitrateKbps: 12000, vbvMilliseconds: 150, threads: 6}
	if cfg.Quality == "balanced" {
		profile = screenQualityProfile{preset: "veryfast", crf: 20, maxBitrateKbps: 8000, vbvMilliseconds: 100, threads: 4}
	}
	if cfg.MaxWidth > 0 && cfg.MaxHeight > 0 && int64(cfg.MaxWidth)*int64(cfg.MaxHeight) <= 1280*720 {
		profile.maxBitrateKbps /= 2
	}
	if cfg.Bitrate > 0 {
		profile.maxBitrateKbps = cfg.Bitrate
	}
	profile.threads = max(1, min(profile.threads, runtime.NumCPU()-1))
	return profile
}
