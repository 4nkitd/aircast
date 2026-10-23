//go:build !windows

package airplay

import (
	"context"
	"fmt"
)

func preparePlatformCapture(context.Context, CaptureConfig, bool) (*CapturePreparation, bool, error) {
	return nil, false, nil
}

func startPlatformCapture(context.Context, context.Context, CaptureConfig, bool, any) (*ScreenCapture, error) {
	return nil, fmt.Errorf("Windows capture is unavailable on this platform")
}
