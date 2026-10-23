package airplay

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestH264PreparationLeadHonorsSessionOrderAndAudioCommit(t *testing.T) {
	for _, test := range []struct {
		name    string
		profile ReceiverProfile
		noAudio bool
		wantErr bool
	}{
		{"modern video only", ReceiverProfileModern, true, false},
		{"modern audio", ReceiverProfileModern, false, false},
		{"legacy video only", ReceiverProfileRoku, true, false},
		{"legacy committed audio", ReceiverProfileRoku, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			SetTargetLatency(0)
			t.Cleanup(func() { SetTargetLatency(0) })
			_, client, ctx := newReceiverServerTestPair(t, ReceiverConfig{Profile: test.profile})
			if err := client.Pair(ctx, ""); err != nil {
				t.Fatal(err)
			}
			if err := client.FairPlaySetup(ctx); err != nil && !errors.Is(err, ErrFairPlayUnsupported) {
				t.Fatal(err)
			}
			session, err := client.SetupMirrorWithCalibratedVideoPreparation(ctx, StreamConfig{VideoCodec: VideoCodecAuto, NoAudio: test.noAudio}, func(_, _ int, codec VideoCodec) (VideoPreparationResult, error) {
				if codec != VideoCodecH264 {
					t.Fatalf("codec = %s", codec)
				}
				return VideoPreparationResult{MinimumVideoLead: 175 * time.Millisecond}, nil
			})
			if test.wantErr {
				if err == nil || !strings.Contains(err.Error(), "media-first audio SETUP already committed") {
					t.Fatalf("late H.264/audio budget was accepted: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			if session.timestampBias != 175*time.Millisecond {
				t.Fatalf("H.264 lead = %v", session.timestampBias)
			}
		})
	}
}
