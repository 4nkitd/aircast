package airplay

import (
	"sync"
	"sync/atomic"
	"testing"
)

func TestAudioCaptureConcurrentStopRunsCleanupOnce(t *testing.T) {
	var cancels atomic.Int32
	done := make(chan struct{})
	close(done)
	capture := &AudioCapture{cancel: func() { cancels.Add(1) }, waitCh: done}
	var workers sync.WaitGroup
	for i := 0; i < 32; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			capture.Stop()
		}()
	}
	workers.Wait()
	if got := cancels.Load(); got != 1 {
		t.Fatalf("capture cleanup ran %d times, want 1", got)
	}
}
