package airplay

// TransmissionStats counts complete VCL access units written to the media
// connection. Bytes excludes codec headers, transport headers and AEAD tags.
// Successful writes do not establish that a physical receiver decoded them.
type TransmissionStats struct {
	Frames uint64
	Bytes  uint64
	Width  int
	Height int
	Codec  VideoCodec
}

func (s *MirrorSession) TransmissionStats() TransmissionStats {
	if s == nil {
		return TransmissionStats{}
	}
	s.statsMu.RLock()
	defer s.statsMu.RUnlock()
	return s.videoStats
}

func (s *MirrorSession) FirstVideoFrame() <-chan struct{} {
	return s.firstFrameSent
}
