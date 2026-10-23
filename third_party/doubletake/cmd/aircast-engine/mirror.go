package main

import (
	"context"
	"errors"
	"math"
	"time"

	"doubletake/internal/airplay"
)

func (s *sender) mirror(auth *authentication) error {
	client, info, store := auth.client, auth.info, auth.store
	_ = s.writer.state("starting", "Preparing screen capture")
	saved := store.lookup(info.DeviceID)
	cfg := airplay.CaptureConfig{
		Quality:      s.start.Quality,
		FPS:          s.start.FPS,
		HWAccel:      "auto",
		VideoCodec:   airplay.VideoCodecAuto,
		ShowCursor:   *s.start.Cursor,
		RestoreToken: saved.RestoreToken,
		SaveRestoreToken: func(token string) error {
			return store.saveRestoreToken(info.DeviceID, token)
		},
	}
	preparation, err := s.deps.prepare(s.ctx, cfg)
	if err != nil {
		return stageError(err, "Screen capture could not start. Check screen permission and the installed media runtime")
	}
	defer preparation.Close()
	streamCfg := airplay.StreamConfig{
		FPS:                    cfg.FPS,
		VideoCodec:             airplay.VideoCodecAuto,
		NoAudio:                !s.start.Audio,
		AutomaticHEVCAvailable: false,
	}
	var audio *airplay.AudioCapture
	var audioCodec airplay.AudioCodec
	if s.start.Audio {
		audioCodec, err = client.ScreenAudioCodec()
		if err == nil {
			audio, err = airplay.StartAudioCapture(s.ctx, false, audioCodec)
		}
		if err != nil {
			streamCfg.NoAudio = true
			s.writer.warn("System audio is unavailable. Sharing video only")
		}
	}
	defer func() {
		if audio != nil {
			audio.Stop()
		}
	}()
	var capture *airplay.ScreenCapture
	var broadcast *airplay.BroadcastCapture
	var broadcastDone chan error
	var selectedCodec airplay.VideoCodec
	var captureWidth, captureHeight int
	var minimumVideoLead time.Duration
	defer func() {
		if capture != nil {
			capture.Stop()
			if broadcastDone != nil {
				<-broadcastDone
			}
		}
	}()
	prepareVideo := func(width, height int, codec airplay.VideoCodec) (airplay.VideoPreparationResult, error) {
		width, height, valid := nominalCanvas(width, height)
		if !valid {
			return airplay.VideoPreparationResult{}, failed("Receiver advertised invalid display dimensions")
		}
		if capture != nil {
			if codec != selectedCodec || width != captureWidth || height != captureHeight {
				return airplay.VideoPreparationResult{}, failed("Receiver changed video format during setup")
			}
			return airplay.VideoPreparationResult{MinimumVideoLead: minimumVideoLead}, nil
		}
		if codec != airplay.VideoCodecH264 {
			return airplay.VideoPreparationResult{}, failed("Receiver selected an unsupported screen codec")
		}
		if cfg.FPS > client.ScreenFrameRateLimit() {
			return airplay.VideoPreparationResult{}, failed("Receiver does not advertise support for the requested frame rate")
		}
		_ = s.writer.emit(event{Event: "state", State: "starting", Message: "Starting encrypted screen sharing", Width: width, Height: height, Codec: string(codec)})
		capture, err = preparation.StartWithCodec(width, height, codec)
		if err != nil {
			return airplay.VideoPreparationResult{}, stageError(err, "Screen capture or video encoding could not start")
		}
		if capture.Backend() == "gdi" {
			s.writer.notice(gdiCaptureNotice)
		}
		minimumVideoLead = preparation.MeasuredVideoLatency()
		if (capture.Backend() == "dxgi" || capture.Backend() == "gdi") && minimumVideoLead == 0 {
			minimumVideoLead, err = airplay.MeasureVideoCaptureLatency(s.ctx, capture, cfg.FPS)
			if err != nil {
				return airplay.VideoPreparationResult{}, stageError(err, "Screen capture cannot meet the bounded video presentation lead")
			}
		}
		selectedCodec = codec
		captureWidth, captureHeight = width, height
		broadcast = airplay.NewBroadcastCaptureWithFrameRate(capture, cfg.FPS)
		broadcastDone = make(chan error, 1)
		go func() { broadcastDone <- broadcast.Run() }()
		return airplay.VideoPreparationResult{MinimumVideoLead: minimumVideoLead}, nil
	}
	var session *airplay.MirrorSession
	for attempt := 0; attempt < maxCredentialAttempts; attempt++ {
		session, err = client.SetupMirrorWithCalibratedVideoPreparation(s.ctx, streamCfg, prepareVideo)
		if err == nil {
			break
		}
		if !credentialRejected(err) || attempt == maxCredentialAttempts-1 || s.ctx.Err() != nil {
			var public *engineError
			if errors.As(err, &public) {
				return public
			}
			return stageError(err, "Receiver could not start an encrypted mirror session")
		}
		if err := auth.askPassword("Enter the receiver's configured password or displayed code"); err != nil {
			return err
		}
		if capture != nil {
			streamCfg.VideoCodec = selectedCodec
		}
		if err := auth.connect(); err != nil {
			return err
		}
		if auth.info.DeviceID != info.DeviceID {
			return failed("Receiver identity changed during setup")
		}
		if err := auth.pair(); err != nil {
			return err
		}
		if err := auth.fairPlay(); err != nil {
			return err
		}
		client = auth.client
	}
	if session == nil {
		return failed("Receiver refused the password")
	}
	defer session.Close()
	if capture == nil || broadcast == nil {
		return failed("Receiver completed setup without video capture")
	}
	if auth.unwatch != nil {
		auth.unwatch()
		auth.unwatch = nil
	}
	sink, err := broadcast.AddBackpressuredSink()
	if err != nil {
		return failed("Could not attach the encoded video stream")
	}
	defer sink.Close()
	if audio != nil && (!session.HasAudio() || session.AudioCodec() != audioCodec) {
		audio.Stop()
		audio = nil
		s.writer.warn("Receiver did not enable the prepared audio format. Sharing video only")
	}
	return s.stream(session, sink, capture, audio)
}

func nominalCanvas(width, height int) (int, int, bool) {
	if width < 2 || height < 2 || width > 8192 || height > 8192 || int64(width)*int64(height) > 33554432 {
		return 0, 0, false
	}
	scale := math.Min(1, math.Min(1920/float64(width), 1080/float64(height)))
	width = int(float64(width)*scale) &^ 1
	height = int(float64(height)*scale) &^ 1
	return width, height, width >= 2 && height >= 2
}

func (s *sender) stream(session *airplay.MirrorSession, sink *airplay.BroadcastSink, capture *airplay.ScreenCapture, audio *airplay.AudioCapture) error {
	ctx, cancel := context.WithCancel(s.ctx)
	videoDone := make(chan struct{})
	videoResult := make(chan error, 1)
	go func() {
		defer close(videoDone)
		videoResult <- streamVideo(ctx, session, sink.AsCapture())
	}()
	var audioDone chan struct{}
	audioResult := make(chan error, 1)
	stopCapture := context.AfterFunc(ctx, func() {
		sink.Close()
		capture.Stop()
	})
	defer func() {
		cancel()
		sink.Close()
		capture.Stop()
		stopCapture()
		if audio != nil {
			audio.Stop()
		}
		<-videoDone
		_ = session.Close()
		if audioDone != nil {
			<-audioDone
		}
		stats := session.TransmissionStats()
		if stats.Frames > 0 {
			_ = s.writer.emit(event{Event: "stats", Frames: stats.Frames, Bytes: stats.Bytes})
		}
	}()
	if audio != nil {
		audioDone = make(chan struct{})
		go func() {
			defer close(audioDone)
			audioResult <- streamAudio(ctx, session, audio)
		}()
	}
	first := session.FirstVideoFrame()
	deadline := time.NewTimer(15 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	streaming := false
	report := func() {
		stats := session.TransmissionStats()
		if stats.Frames == 0 || ctx.Err() != nil {
			return
		}
		if !streaming {
			streaming = true
			deadline.Stop()
			_ = s.writer.emit(event{Event: "state", State: "streaming", Message: "Screen frames are being transmitted", Width: stats.Width, Height: stats.Height, Codec: string(stats.Codec)})
		}
		_ = s.writer.emit(event{Event: "stats", Frames: stats.Frames, Bytes: stats.Bytes})
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-first:
			first = nil
			report()
		case <-deadline.C:
			if !streaming {
				return failed("No screen frames reached the receiver within 15 seconds")
			}
		case <-ticker.C:
			report()
		case <-audioResult:
			if ctx.Err() == nil {
				s.writer.warn("System audio stopped. Sharing video only")
			}
			audioResult = nil
		case err := <-videoResult:
			if ctx.Err() != nil {
				return ctx.Err()
			}
			report()
			return stageError(err, "Screen frame transmission stopped unexpectedly")
		}
	}
}

func streamVideo(ctx context.Context, session *airplay.MirrorSession, capture *airplay.ScreenCapture) (err error) {
	defer func() {
		if recover() != nil {
			err = failed("Malformed encoded video data")
		}
	}()
	return session.StreamFrames(ctx, capture, 0)
}

func streamAudio(ctx context.Context, session *airplay.MirrorSession, capture *airplay.AudioCapture) (err error) {
	defer func() {
		if recover() != nil {
			err = failed("Malformed audio data")
		}
	}()
	return session.StreamAudio(ctx, capture, session.AudioStream())
}
