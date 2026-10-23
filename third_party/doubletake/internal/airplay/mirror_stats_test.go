package airplay

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"golang.org/x/crypto/chacha20poly1305"
)

type statsTestConnection struct {
	bytes int
	fail  bool
	short bool
}

func (c *statsTestConnection) Read([]byte) (int, error) { return 0, io.EOF }
func (c *statsTestConnection) Write(data []byte) (int, error) {
	if c.fail {
		return 0, io.ErrClosedPipe
	}
	n := len(data)
	if c.short {
		n /= 2
	}
	c.bytes += n
	return n, nil
}
func (*statsTestConnection) Close() error                     { return nil }
func (*statsTestConnection) LocalAddr() net.Addr              { return nil }
func (*statsTestConnection) RemoteAddr() net.Addr             { return nil }
func (*statsTestConnection) SetDeadline(time.Time) error      { return nil }
func (*statsTestConnection) SetReadDeadline(time.Time) error  { return nil }
func (*statsTestConnection) SetWriteDeadline(time.Time) error { return nil }

func TestTransmissionStatsExcludeUnwrittenAndNonPicturePackets(t *testing.T) {
	connection := &statsTestConnection{}
	session := &MirrorSession{dataConn: connection, videoWidth: 1920, videoHeight: 1080}
	if err := session.sendCodecFrame([]byte{1, 2, 3}, 1); err != nil {
		t.Fatal(err)
	}
	if got := session.TransmissionStats(); got.Frames != 0 || got.Bytes != 0 {
		t.Fatalf("codec packet counted as a frame: %+v", got)
	}
	if err := session.sendFrame([]byte{1, 2, 3, 4, 5}, true, 2, 0); err != nil {
		t.Fatal(err)
	}
	want := session.TransmissionStats()
	if want.Frames != 1 || want.Bytes != 5 || want.Width != 1920 || want.Height != 1080 || want.Codec != VideoCodecH264 {
		t.Fatalf("wrong transmitted counters: %+v", want)
	}
	connection.fail = true
	if err := session.sendFrame([]byte{6}, false, 3, 0); err == nil {
		t.Fatal("failed socket write succeeded")
	}
	if got := session.TransmissionStats(); got != want {
		t.Fatalf("failed write changed stats: %+v", got)
	}
	connection.fail, connection.short = false, true
	if err := session.sendFrame([]byte{6}, false, 4, 0); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("short write error = %v", err)
	}
	if got := session.TransmissionStats(); got != want {
		t.Fatalf("partial write counted: %+v", got)
	}
}

func TestTransmissionStatsExcludeAEADOverhead(t *testing.T) {
	cipher, err := chacha20poly1305.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	connection := &statsTestConnection{}
	session := &MirrorSession{dataConn: connection, chachaCipher: cipher}
	if err := session.sendFrame(make([]byte, 100), true, 1, 0); err != nil {
		t.Fatal(err)
	}
	if got := session.TransmissionStats(); got.Frames != 1 || got.Bytes != 100 || connection.bytes != 128+100+cipher.Overhead() {
		t.Fatalf("wrong encrypted payload counters: %+v wire=%d", got, connection.bytes)
	}
}

func TestMirrorCloseInterruptsBlockedDataWorker(t *testing.T) {
	connection, receiver := net.Pipe()
	defer receiver.Close()
	defer connection.Close()
	_, cancel := context.WithCancel(context.Background())
	session := &MirrorSession{dataConn: connection, cancel: cancel}
	started := make(chan struct{})
	session.startWorker(func() {
		close(started)
		_, _ = connection.Write([]byte{1})
	})
	<-started
	closed := make(chan error, 1)
	go func() { closed <- session.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close waited for a blocked media writer before closing its connection")
	}
}
