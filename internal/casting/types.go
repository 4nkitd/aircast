package casting

type State string

const (
	Idle       State = "idle"
	Connecting State = "connecting"
	Pairing    State = "pairing"
	Starting   State = "starting"
	Streaming  State = "streaming"
	Stopping   State = "stopping"
	Failed     State = "failed"
)

const BestQuality = "quality"
const Balanced = "balanced"

type Options struct {
	Quality    string `json:"quality"`
	FPS        int    `json:"fps"`
	Audio      bool   `json:"audio"`
	ShowCursor bool   `json:"cursor"`
}

type Event struct {
	Type           string `json:"event"`
	State          State  `json:"state,omitempty"`
	Message        string `json:"message,omitempty"`
	CredentialKind string `json:"kind,omitempty"`
	DeviceID       string `json:"device_id,omitempty"`
	DeviceName     string `json:"device_name,omitempty"`
	Width          int    `json:"width,omitempty"`
	Height         int    `json:"height,omitempty"`
	Codec          string `json:"codec,omitempty"`
	Frames         uint64 `json:"frames,omitempty"`
	Bytes          uint64 `json:"bytes,omitempty"`
}
