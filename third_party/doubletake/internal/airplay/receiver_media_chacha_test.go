package airplay

import (
	"bytes"
	"testing"

	"golang.org/x/crypto/chacha20poly1305"
)

func TestReceiverMediaAuthenticatesChaChaVideoFrames(t *testing.T) {
	for _, corrupt := range []bool{false, true} {
		name := "authenticated"
		if corrupt {
			name = "wrong key"
		}
		t.Run(name, func(t *testing.T) {
			key := bytes.Repeat([]byte{0x43}, 32)
			receiver := newTestReceiverMediaSession(t, receiverMediaConfig{})
			if err := receiver.configureVideoChaCha(key); err != nil {
				t.Fatal(err)
			}
			if corrupt {
				key[0] ^= 1
			}
			cipher, err := chacha20poly1305.New(key)
			if err != nil {
				t.Fatal(err)
			}
			connection := dialReceiverMediaTCP(t, receiver.Endpoints().VideoPort)
			defer connection.Close()
			sender := &MirrorSession{dataConn: connection, chachaCipher: cipher, videoWidth: 640, videoHeight: 360}
			if err := sender.sendCodecFrame([]byte{1, 2, 3}, 1); err != nil {
				t.Fatal(err)
			}
			if err := sender.sendFrame(receiverTestAVCC([]byte{0x65, 0x80}), true, 2, 0); err != nil {
				t.Fatal(err)
			}
			waitForReceiverMediaStats(t, receiver, func(stats receiverMediaStats) bool {
				return stats.VideoDecrypted > 0 || stats.VideoCryptoErrors > 0
			})
			stats := receiver.Snapshot()
			if corrupt {
				if stats.VideoCryptoErrors != 1 || stats.VideoDecrypted != 0 {
					t.Fatalf("corrupt media was accepted: %+v", stats)
				}
			} else if stats.VideoCryptoErrors != 0 || stats.VideoDecrypted != 1 || stats.VideoMalformed != 0 {
				t.Fatalf("authenticated media failed: %+v", stats)
			}
		})
	}
}
