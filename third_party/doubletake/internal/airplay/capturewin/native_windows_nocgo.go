//go:build windows && !cgo

package capturewin

import "fmt"

type Capture struct{}

func Primary() (Monitor, error) {
	return Monitor{}, fmt.Errorf("Windows screen capture requires a CGO-enabled build")
}

func Open(Monitor, bool) (*Capture, error) {
	return nil, fmt.Errorf("Windows screen capture requires a CGO-enabled build")
}

func (*Capture) GDI() bool { return false }

func (*Capture) Frame([]byte) (bool, error) {
	return false, fmt.Errorf("Windows screen capture requires a CGO-enabled build")
}

func (*Capture) Close() {}
