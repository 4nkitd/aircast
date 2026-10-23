package capturewin

type Monitor struct {
	X, Y, Width, Height       int
	AdapterLow                uint32
	AdapterHigh               int32
	AdapterIndex, OutputIndex uint32
	Handle                    uintptr
}
