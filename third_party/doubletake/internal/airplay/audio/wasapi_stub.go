//go:build !windows || !cgo

package audio

import (
	"context"
	"fmt"
)

func StartLoopback(ctx context.Context, frameSamples int) (*Capture, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("Windows WASAPI loopback is unavailable in this build; it requires Windows with CGO_ENABLED=1 and a Windows C compiler")
}
