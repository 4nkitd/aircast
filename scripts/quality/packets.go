package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type packetStats struct {
	Frames         int     `json:"frames"`
	Keyframes      int     `json:"keyframes"`
	PayloadBytes   int64   `json:"payload_bytes"`
	LargestAUBytes int64   `json:"largest_access_unit_bytes"`
	MeanMbps       float64 `json:"mean_video_mbps"`
	Log            string  `json:"log"`
}

func (r *runner) checkPackets(name string) error {
	data, err := r.command(r.cfg.FFprobe, "-v", "error", "-select_streams", "v:0", "-show_packets", "-show_entries", "packet=pts_time,dts_time,duration_time,size,flags", "-of", "json", name)
	if err != nil {
		return err
	}
	var result struct {
		Packets []struct {
			PTS      string `json:"pts_time"`
			DTS      string `json:"dts_time"`
			Duration string `json:"duration_time"`
			Size     string `json:"size"`
			Flags    string `json:"flags"`
		} `json:"packets"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return err
	}
	stats := packetStats{Log: name + "-packets.json"}
	last := -1.0
	for i, p := range result.Packets {
		pts, e1 := strconv.ParseFloat(p.PTS, 64)
		dts, e2 := strconv.ParseFloat(p.DTS, 64)
		size, e3 := strconv.ParseInt(p.Size, 10, 64)
		if e1 != nil || e2 != nil || e3 != nil || math.IsNaN(pts) || math.IsInf(pts, 0) || pts < 0 || pts <= last || pts != dts || size <= 0 {
			return fmt.Errorf("%s packet %d: invalid/missing/reordered timestamp or payload", name, i)
		}
		if i == 0 && !strings.Contains(p.Flags, "K") {
			return fmt.Errorf("%s: first packet is not a keyframe", name)
		}
		last = pts
		stats.Frames++
		stats.PayloadBytes += size
		stats.LargestAUBytes = max(stats.LargestAUBytes, size)
		if strings.Contains(p.Flags, "K") {
			stats.Keyframes++
		}
	}
	if stats.Frames != r.cfg.Frames {
		return fmt.Errorf("%s: packet count %d, expected %d", name, stats.Frames, r.cfg.Frames)
	}
	stats.MeanMbps = float64(stats.PayloadBytes) * 8 * float64(r.cfg.FPS) / float64(r.cfg.Frames) / 1e6
	if err := os.WriteFile(filepath.Join(r.cfg.Out, stats.Log), data, 0644); err != nil {
		return err
	}
	r.report.PacketStats[name] = stats
	return nil
}

func checkEncoderSettings(output string, p profile, gop int) error {
	values := map[string]string{}
	for _, field := range strings.Fields(output) {
		pair := strings.SplitN(field, "=", 2)
		if len(pair) == 2 {
			values[pair[0]] = pair[1]
		}
	}
	mbps, err := strconv.Atoi(strings.TrimSuffix(p.MaxRate, "M"))
	if err != nil {
		return err
	}
	for key, want := range map[string]string{"rc": "crf", "crf": fmt.Sprintf("%d.0", p.CRF), "vbv_maxrate": strconv.Itoa(mbps * 1000), "vbv_bufsize": strings.TrimSuffix(p.Buffer, "k"), "bframes": "0", "keyint": strconv.Itoa(gop)} {
		if values[key] != want {
			return fmt.Errorf("effective x264 %s=%q, expected %s", key, values[key], want)
		}
	}
	threads, err := strconv.Atoi(values["threads"])
	if err != nil || threads < 1 || threads > 2 {
		return errors.New("effective x264 thread count is not in 1..2")
	}
	return nil
}
