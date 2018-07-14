package discordvoice

import (
	"io"
	"time"

	"github.com/jeffreymkabot/discordvoice"
	"github.com/jonas747/dca"
)

type Source struct {
	r   io.Reader
	enc *dca.EncodeSession
}

func NewSource(r io.Reader, opts *dca.EncodeOptions) (*Source, error) {
	enc, err := dca.EncodeMem(r, opts)
	if err != nil {
		return nil, err
	}
	return &Source{r: r, enc: enc}, nil
}

func (s *Source) ReadFrame() ([]byte, error) {
	return s.enc.OpusFrame()
}

func (s *Source) FrameDuration() time.Duration {
	return s.enc.FrameDuration()
}

func (s *Source) Close() error {
	s.enc.Cleanup()
	if rc, ok := s.r.(io.Closer); ok {
		return rc.Close()
	}
	return nil
}

var _ player.SourceCloser = &Source{}
