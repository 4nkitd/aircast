//go:build windows && cgo

package capturewin

/*
#cgo LDFLAGS: -ld3d11 -ldxgi -ldxguid -lgdi32 -luser32
#include "native.h"
*/
import "C"

import (
	"fmt"
	"runtime"
	"unsafe"
)

type Capture struct {
	ptr *C.AircastCapture
}

func Primary() (Monitor, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	var info C.AircastMonitor
	var message [512]C.char
	if C.aircast_primary_monitor(&info, &message[0], C.size_t(len(message))) == 0 {
		return Monitor{}, fmt.Errorf("Windows primary display: %s", C.GoString(&message[0]))
	}
	return Monitor{
		X: int(info.x), Y: int(info.y), Width: int(info.width), Height: int(info.height),
		AdapterLow: uint32(info.adapter_low), AdapterHigh: int32(info.adapter_high),
		AdapterIndex: uint32(info.adapter_index), OutputIndex: uint32(info.output_index), Handle: uintptr(info.monitor),
	}, nil
}

func Open(monitor Monitor, cursor bool) (*Capture, error) {
	info := C.AircastMonitor{
		x: C.int(monitor.X), y: C.int(monitor.Y), width: C.int(monitor.Width), height: C.int(monitor.Height),
		adapter_low: C.uint32_t(monitor.AdapterLow), adapter_high: C.int32_t(monitor.AdapterHigh),
		adapter_index: C.uint(monitor.AdapterIndex), output_index: C.uint(monitor.OutputIndex), monitor: C.uintptr_t(monitor.Handle),
	}
	showCursor := C.int(0)
	if cursor {
		showCursor = 1
	}
	var message [512]C.char
	ptr := C.aircast_capture_open(&info, showCursor, &message[0], C.size_t(len(message)))
	if ptr == nil {
		return nil, fmt.Errorf("Windows capture: %s", C.GoString(&message[0]))
	}
	return &Capture{ptr: ptr}, nil
}

func (c *Capture) GDI() bool {
	return C.aircast_capture_is_gdi(c.ptr) != 0
}

func (c *Capture) Frame(pixels []byte) (bool, error) {
	if len(pixels) == 0 || c.ptr == nil {
		return false, fmt.Errorf("Windows capture is closed or its pixel buffer is empty")
	}
	var message [512]C.char
	status := C.aircast_capture_frame(c.ptr, (*C.uchar)(unsafe.Pointer(&pixels[0])), C.size_t(len(pixels)), &message[0], C.size_t(len(message)))
	if status < 0 {
		return false, fmt.Errorf("Windows capture: %s", C.GoString(&message[0]))
	}
	return status == 1, nil
}

func (c *Capture) Close() {
	if c.ptr != nil {
		C.aircast_capture_close(c.ptr)
		c.ptr = nil
	}
}
