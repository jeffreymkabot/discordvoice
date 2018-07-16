package mp3

import (
	"io"
	"io/ioutil"
	"time"

	mp3 "github.com/hajimehoshi/go-mp3"
	"github.com/jeffreymkabot/discordvoice"
)

// go-mp3 constants
const (
	bytesPerSample = 4
	// from github.com/hajimehoshi/go-mp3/internal/consts
	bytesPerFrame = 4608
)

// SourceCloser provides a source of decoded PCM frames from an mp3.
type SourceCloser struct {
	decoder *mp3.Decoder
}

// NewSource produces a source of decoded PCM frames from an mp3.
// If the reader implements io.Closer the reader will be closed when the source is closed.
func NewSource(r io.Reader) (*SourceCloser, error) {
	rc, ok := r.(io.ReadCloser)
	if !ok {
		rc = ioutil.NopCloser(r)
	}

	dec, err := mp3.NewDecoder(rc)
	if err != nil {
		return nil, err
	}

	return &SourceCloser{decoder: dec}, nil
}

// ReadFrame implements player.SourceCloser.
func (src *SourceCloser) ReadFrame() (frame []byte, err error) {
	frame = make([]byte, bytesPerFrame)
	nr, err := src.decoder.Read(frame)
	frame = frame[0:nr]
	return
}

// FrameDuration implements player.SourceCloser.
func (src *SourceCloser) FrameDuration() time.Duration {
	bytesPerSecond := bytesPerSample * src.decoder.SampleRate()
	secondsPerFrame := float64(bytesPerFrame) / float64(bytesPerSecond)
	return time.Duration(secondsPerFrame * float64(time.Second))
}

// Close implements player.SourceCloser.
func (src *SourceCloser) Close() error {
	// go-mp3 calls close on the underlying reader
	return src.decoder.Close()
}

// do not compile unless SourceCloser implements player.SourceCloser
var _ player.SourceCloser = &SourceCloser{}
