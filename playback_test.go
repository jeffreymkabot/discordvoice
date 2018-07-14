package player_test

import (
	"io"
	"os"
	"testing"
	"time"

	"github.com/hajimehoshi/oto"
	"github.com/jeffreymkabot/discordvoice"
	"github.com/jeffreymkabot/discordvoice/mp3"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPlayback(t *testing.T) {
	t.Parallel()

	openSource := func() (player.Source, error) {
		f, err := os.Open("media/test_file.mp3")
		if err != nil {
			return nil, err
		}
		return mp3.NewSource(f)
	}

	bufferSize := 1 << 15
	openDevice := func() (io.Writer, error) {
		return oto.NewPlayer(44100, 2, 2, bufferSize)
	}

	end := make(chan struct{})

	p := player.New()
	defer p.Close()
	p.Enqueue("test", openSource, openDevice,
		player.OnStart(func() {
			t.Log("playback started")
		}),
		player.OnEnd(func(e time.Duration, err error) {
			t.Logf("playback stopped after %v seconds because %v", e.Seconds(), err)
			assert.InDelta(t, 21, e.Seconds(), 0.5, "expected elapsed to be roughly 21 seconds")
			assert.Equal(t, errors.Cause(err), io.EOF, "expected playback to end because of EOF")
			close(end)
		}),
	)

	select {
	case <-end:
	case <-time.After(25 * time.Second):
		require.FailNow(t, "timeout after 25 seconds")
	}
}
