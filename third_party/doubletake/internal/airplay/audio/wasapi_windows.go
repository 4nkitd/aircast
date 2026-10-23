//go:build windows && cgo

package audio

/*
#cgo CFLAGS: -std=c11 -Wall -Wextra
#cgo LDFLAGS: -lole32
#include "wasapi_windows.h"
*/
import "C"

import (
	"context"
	"fmt"
	"runtime"
	"time"
	"unsafe"
)

const (
	wasapiDiscontinuity  = 1
	wasapiSilent         = 2
	wasapiTimestampError = 4
	wasapiIdleTimeout    = 5 * time.Second
)

func StartLoopback(ctx context.Context, frameSamples int) (*Capture, error) {
	capture, err := newCapture(ctx, frameSamples)
	if err != nil {
		return nil, err
	}
	ready := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		capture.finish(capture.loopback(ready))
	}()
	select {
	case err := <-ready:
		if err != nil {
			capture.Close()
			return nil, err
		}
		return capture, nil
	case <-ctx.Done():
		capture.Close()
		return nil, ctx.Err()
	}
}

func (c *Capture) loopback(ready chan<- error) error {
	var native *C.ac_wasapi
	var format C.ac_wasapi_format
	var message [256]C.char
	if C.ac_wasapi_open(&native, &format, &message[0], C.size_t(len(message))) != 0 {
		err := fmt.Errorf("WASAPI loopback: %s", C.GoString(&message[0]))
		ready <- err
		return err
	}
	defer C.ac_wasapi_close(native)
	decoded := mixFormat{rate: int(format.rate), channels: int(format.channels), bits: int(format.bits),
		validBits: int(format.valid_bits), blockAlign: int(format.block_align),
		floating: format.floating != 0, channelMask: uint32(format.channel_mask)}
	converter, err := newConverter(decoded, c.frameSamples, c.emit)
	if err != nil {
		ready <- err
		return err
	}
	bytes := uint64(format.buffer_frames) * uint64(format.block_align)
	if bytes == 0 || bytes > maxPacketBytes {
		err := fmt.Errorf("WASAPI loopback buffer exceeds %d bytes", maxPacketBytes)
		ready <- err
		return err
	}
	buffer := make([]byte, int(bytes))
	before := time.Now()
	anchorQPC := uint64(C.ac_wasapi_qpc_100ns())
	anchor := before.Add(time.Since(before) / 2)
	if anchorQPC == 0 {
		err := fmt.Errorf("WASAPI loopback could not read the Windows performance clock")
		ready <- err
		return err
	}
	if err := c.ctx.Err(); err != nil {
		ready <- err
		return err
	}
	ready <- nil
	poll := time.NewTicker(5 * time.Millisecond)
	defer poll.Stop()
	lastPacket := time.Now()
	clock := captureClock{qpc: anchorQPC, pts: anchor, rate: decoded.rate}
	for {
		if err := c.ctx.Err(); err != nil {
			return err
		}
		var packet C.ac_wasapi_packet
		status := C.ac_wasapi_read(native, (*C.uchar)(unsafe.Pointer(&buffer[0])), C.size_t(len(buffer)),
			&packet, &message[0], C.size_t(len(message)))
		if status < 0 {
			return fmt.Errorf("WASAPI loopback: %s", C.GoString(&message[0]))
		}
		if status == 0 {
			if time.Since(lastPacket) >= wasapiIdleTimeout {
				return fmt.Errorf("WASAPI default-output loopback produced no packets for %s; the render device may be idle or unavailable (no microphone or synthetic-silence fallback)", wasapiIdleTimeout)
			}
			select {
			case <-c.ctx.Done():
				return c.ctx.Err()
			case <-poll.C:
			}
			continue
		}
		lastPacket = time.Now()
		flags := uint32(packet.flags)
		source, err := clock.stamp(sourcePacket{data: buffer[:int(packet.frames)*decoded.blockAlign],
			frames: int(packet.frames), position: uint64(packet.device_position),
			silent: flags&wasapiSilent != 0, discontinuity: flags&wasapiDiscontinuity != 0},
			uint64(packet.qpc_100ns), flags&wasapiTimestampError == 0, lastPacket)
		if err != nil {
			return err
		}
		if err := converter.push(source); err != nil {
			return err
		}
	}
}
