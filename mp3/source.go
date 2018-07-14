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

type MP3Source struct {
	decoder *mp3.Decoder
}

func NewSource(r io.Reader) (*MP3Source, error) {
	rc, ok := r.(io.ReadCloser)
	if !ok {
		rc = ioutil.NopCloser(r)
	}

	dec, err := mp3.NewDecoder(rc)
	if err != nil {
		return nil, err
	}

	return &MP3Source{decoder: dec}, nil
}

func (src *MP3Source) ReadFrame() (frame []byte, err error) {
	frame = make([]byte, bytesPerFrame)
	nr, err := src.decoder.Read(frame)
	frame = frame[0:nr]
	return
}

func (src *MP3Source) FrameDuration() time.Duration {
	bytesPerSecond := bytesPerSample * src.decoder.SampleRate()
	secondsPerFrame := float64(bytesPerFrame) / float64(bytesPerSecond)
	return time.Duration(secondsPerFrame * float64(time.Second))
}

func (src *MP3Source) Close() error {
	// go-mp3 calls close on the underlying reader
	return src.decoder.Close()
}

var _ player.SourceCloser = &MP3Source{}
