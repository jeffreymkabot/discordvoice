package player_test

import (
	"io"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	mp3 "github.com/hajimehoshi/go-mp3"
	"github.com/hajimehoshi/oto"
	"github.com/jeffreymkabot/discordvoice"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
)

// go-mp3 constants
const (
	bytesPerSample = 4
	// from github.com/hajimehoshi/go-mp3/internal/consts
	bytesPerFrame = 4608
)

type mp3Source struct {
	decoder *mp3.Decoder
}

func (src mp3Source) ReadFrame() (frame []byte, err error) {
	frame = make([]byte, bytesPerFrame)
	nr, err := src.decoder.Read(frame)
	frame = frame[0:nr]
	return
}

func (src mp3Source) FrameDuration() time.Duration {
	bytesPerSecond := bytesPerSample * src.decoder.SampleRate()
	secondsPerFrame := float64(bytesPerFrame) / float64(bytesPerSecond)
	return time.Duration(secondsPerFrame * float64(time.Second))
}

func TestPlayback(t *testing.T) {
	p := player.New()
	defer p.Close()

	openSong := func() (io.Reader, error) {
		return os.Open("test.mp3")
	}
	encodeSong := func(r io.Reader) (player.Source, error) {
		decoder, err := mp3.NewDecoder(r.(io.ReadCloser))
		if err != nil {
			return nil, err
		}
		return mp3Source{decoder: decoder}, nil
	}

	bufferSize := 1 << 15
	openDevice := func() (io.Writer, error) {
		return oto.NewPlayer(44100, 2, 2, bufferSize)
	}

	end := make(chan struct{})
	p.Enqueue("test", openSong, openDevice,
		player.Encoder(encodeSong),
		player.OnStart(func() {
			t.Log("playback started")
		}),
		player.OnEnd(func(e time.Duration, err error) {
			t.Logf("playback stopped after %v because %v", e, err)
			assert.Equal(t, errors.Cause(err), io.EOF, "expected playback to end because of EOF")
			close(end)
		}),
	)

	select {
	case <-end:
	case <-time.After(5 * time.Second):
		require.FailNow(t, "timeout after 5 seconds")
	}
}
