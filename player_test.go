package player_test

import (
	"io"
	"io/ioutil"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jeffreymkabot/discordvoice"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type nopWriterOpener struct{}

func (o *nopWriterOpener) Open(x string) (io.WriteCloser, error) {
	return nopWriter{ioutil.Discard}, nil
}

type nopWriter struct {
	io.Writer
}

func (w nopWriter) Close() error { return nil }

var nopSongOpener player.SongOpenerFunc = func() (io.ReadCloser, error) {
	return ioutil.NopCloser(strings.NewReader("hello world")), nil
}

func TestCallbacks(t *testing.T) {
	t.Parallel()
	p := player.New(&nopWriterOpener{}, player.QueueLength(1))
	require.NotNil(t, p)
	defer p.Close()

	var waitForPause sync.WaitGroup
	var waitForEnd sync.WaitGroup
	waitForPause.Add(1)
	waitForEnd.Add(1)

	var calledOnStart, calledOnPause, calledOnResume, calledOnProgress, calledOnEnd bool
	var pauseTime time.Duration
	var resumeTime time.Duration
	var endErr error
	err := p.Enqueue("", "", nopSongOpener,
		player.PreEncoded(),
		player.OnStart(func() {
			calledOnStart = true
			p.Pause()
		}),
		player.OnPause(func(elapsed time.Duration) {
			calledOnPause = true
			// song should have paused itself in OnStart
			pauseTime = elapsed
			waitForPause.Done()

		}),
		player.OnResume(func(elapsed time.Duration) {
			calledOnResume = true
			resumeTime = elapsed

		}),
		player.OnProgress(func(elapsed time.Duration, times []time.Duration) {
			calledOnProgress = true
		}, 0),
		player.OnEnd(func(elapsed time.Duration, err error) {
			calledOnEnd = true
			endErr = errors.Cause(err)
			waitForEnd.Done()
		}),
	)
	require.NoError(t, err, "failed to queue song")
	waitForPause.Wait()

	assert.True(t, calledOnStart, "did not call OnStart callback")
	assert.True(t, calledOnPause, "did not call OnPause callback")
	p.Pause()

	waitForEnd.Wait()

	assert.True(t, calledOnResume, "did not call OnResume callback")
	assert.False(t, calledOnProgress, "called OnProgress when passed invalid progress interval")
	assert.True(t, calledOnEnd, "did not call OnEnd callback")
	assert.Zero(t, pauseTime, "song should pause immediately on start")
	assert.Equal(t, pauseTime, resumeTime, "should should have no progress between pause and resume")
	assert.Contains(t, []error{io.EOF, io.ErrUnexpectedEOF}, endErr, "song should read/write until EOF")
}

func TestSkip(t *testing.T) {
	t.Parallel()
	p := player.New(&nopWriterOpener{}, player.QueueLength(1))
	require.NotNil(t, p)
	defer p.Close()

	var endErr error
	var waitForPause sync.WaitGroup
	var waitForEnd sync.WaitGroup
	waitForPause.Add(1)
	waitForEnd.Add(1)
	err := p.Enqueue("", "", nopSongOpener,
		player.PreEncoded(),
		player.OnStart(func() {
			p.Pause()
		}),
		player.OnPause(func(_ time.Duration) {
			waitForPause.Done()
		}),
		player.OnEnd(func(_ time.Duration, err error) {
			endErr = errors.Cause(err)
			waitForEnd.Done()
		}),
	)
	require.NoError(t, err)
	waitForPause.Wait()

	p.Skip()
	waitForEnd.Wait()

	assert.Equal(t, player.ErrSkipped, endErr, "skipping a paused song should end the song")
}
