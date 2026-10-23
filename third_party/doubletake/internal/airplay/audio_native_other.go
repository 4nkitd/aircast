//go:build !windows

package airplay

import "context"

func startNativeAudioCapture(context.Context, bool, AudioCodec) (*AudioCapture, bool, error) {
	return nil, false, nil
}
