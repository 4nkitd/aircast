package airplay

import (
	"context"
	"fmt"
	"os"
	"time"

	nativeaudio "doubletake/internal/airplay/audio"
)

func startNativeAudioCapture(ctx context.Context, testTone bool, codec AudioCodec) (*AudioCapture, bool, error) {
	if codec != AudioCodecALAC && codec != AudioCodecAACELD {
		return nil, true, fmt.Errorf("unsupported audio codec %d", codec)
	}
	if !testTone && os.Getenv("AIRCAST_EXPERIMENTAL_WASAPI_LOOPBACK") != "1" {
		return nil, true, fmt.Errorf("Windows WASAPI loopback is experimental and disabled; set AIRCAST_EXPERIMENTAL_WASAPI_LOOPBACK=1 to opt in to unverified system-output capture")
	}
	_, spf, _, _, _, _ := codec.Info()
	ac := &AudioCapture{codec: codec, waitCh: make(chan struct{})}
	if codec == AudioCodecALAC {
		ac.alac = &alacEncoder{}
	} else {
		var err error
		ac.eld, err = newELDEncoder()
		if err != nil {
			return nil, true, err
		}
	}
	var capture *nativeaudio.Capture
	var err error
	if testTone {
		capture, err = nativeaudio.StartTestTone(ctx, int(spf))
	} else {
		capture, err = nativeaudio.StartLoopback(ctx, int(spf))
	}
	if err != nil {
		if ac.eld != nil {
			ac.eld.Close()
		}
		return nil, true, err
	}
	ac.cancel = capture.Close
	ac.pcmFrames = &windowsAudioPCMReader{capture: capture}
	go func() {
		ac.waitErr = capture.Wait()
		close(ac.waitCh)
	}()
	return ac, true, nil
}

type windowsAudioPCMReader struct {
	capture *nativeaudio.Capture
}

func (r *windowsAudioPCMReader) ReadPCMFrame(dst []byte) (time.Time, error) {
	position, err := r.ReadPCMFramePosition(dst)
	return position.PTS, err
}

func (r *windowsAudioPCMReader) ReadPCMFramePosition(dst []byte) (audioPCMFramePosition, error) {
	position, err := r.capture.ReadFrame(dst)
	if err != nil {
		return audioPCMFramePosition{}, err
	}
	return audioPCMFramePosition{
		PTS: position.PTS, SourceRTP: uint32(position.Sample), HasSourceRTP: true,
	}, nil
}
