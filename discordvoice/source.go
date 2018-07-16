package discordvoice

import (
	"io"
	"time"

	"github.com/jeffreymkabot/discordvoice"
	"github.com/jonas747/dca"
)

// SourceCloser provides a source of opus frames suitable for a discord voice channel.
type SourceCloser struct {
	r   io.Reader
	enc *dca.EncodeSession
}

// NewSource produces a source of opus frames suitable for a discord voice channel.
// The opus encoder requires ffmpeg available in the PATH.
// If the reader implements io.Closer the reader will be closed when the source is closed.
func NewSource(r io.Reader, opts *dca.EncodeOptions) (*SourceCloser, error) {
	enc, err := dca.EncodeMem(r, opts)
	if err != nil {
		return nil, err
	}
	return &SourceCloser{r: r, enc: enc}, nil
}

// ReadFrame implements player.SourceCloser.
func (s *SourceCloser) ReadFrame() ([]byte, error) {
	return s.enc.OpusFrame()
}

// FrameDuration implements player.SourceCloser.
func (s *SourceCloser) FrameDuration() time.Duration {
	return s.enc.FrameDuration()
}

// Close implements player.SourceCloser.
func (s *SourceCloser) Close() error {
	s.enc.Cleanup()
	if rc, ok := s.r.(io.Closer); ok {
		return rc.Close()
	}
	return nil
}

// do no compile unless SourceCloser implements player.SourceCloser.
var _ player.SourceCloser = &SourceCloser{}
