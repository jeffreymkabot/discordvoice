package player

import (
	"io"
	"io/ioutil"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type nopDevice struct{}

func (d *nopDevice) Open(x string) (io.WriteCloser, error) {
	return nopWriter{ioutil.Discard}, nil
}

type nopWriter struct {
	io.Writer
}

func (w nopWriter) Close() error { return nil }

var nopSongOpener SongOpenerFunc = func() (io.ReadCloser, error) {
	return ioutil.NopCloser(strings.NewReader("hello world")), nil
}

func TestNewPlayer(t *testing.T) {
	t.Parallel()
	calledIdle := false
	p := New(&nopDevice{}, IdleFunc(func() { calledIdle = true }, 1))
	require.NotNil(t, p)
	defer p.Close()

	assert.True(t, calledIdle, "did not call idle func")
	assert.Empty(t, p.Playlist(), "playlist is not empty")
}

func TestEnqueuePoll(t *testing.T) {
	t.Parallel()
	p := New(&nopDevice{}, QueueLength(1))
	require.NotNil(t, p)
	defer p.Close()
	require.Empty(t, p.queue)

	pauseAndBlock := "pause and block playback"
	enqueueOne := "enqueue one"
	failToQueue := "fail to queue into full queue"
	passToFirstPoller := "pass directly to first poller"
	passToSecondPoller := "pass directly to second poller"
	ignoreDeadPoller := "do not pass to timed out poller"

	// queue a song and immediately pause it to freeze playback and prevent queue from being consumed
	// wait for it to be paused
	var wg sync.WaitGroup
	wg.Add(1)
	err := p.Enqueue("", pauseAndBlock, nopSongOpener,
		PreEncoded(),
		OnStart(func() {
			p.Pause()
		}),
		OnPause(func(_ time.Duration) {
			wg.Done()
		}))
	require.NoError(t, err, "failed to queue a song into empty queue")
	wg.Wait()
	require.Empty(t, p.queue, "expected queue to be empty after the only queued song has started")

	// queue a song
	err = p.Enqueue("", enqueueOne, nil)
	require.NoError(t, err, "failed to queue a song into empty queue")
	assert.Len(t, p.queue, 1)

	// queue should be full
	err = p.Enqueue("", failToQueue, nil)
	require.Error(t, err)
	assert.Equal(t, ErrFull, err)

	// remove the queued song
	sng, err := p.poll(1)
	require.NoError(t, err, "failed to poll item from non-empty queue")
	assert.Equal(t, enqueueOne, sng.title)
	require.Empty(t, p.queue, "expected queue to be empty after polling the only queued song")

	// set up two routines that poll indefinitely for queued songs
	// wait for poller goroutines to begin execution
	// make sure poller goroutines execute in order
	wg.Add(1)
	go func() {
		wg.Done()
		sng, err := p.poll(0)
		require.NoError(t, err, "failed to poll item from queue")
		assert.Equal(t, passToFirstPoller, sng.title)
		wg.Done()
	}()
	wg.Wait()    // give a chance for the first poller goroutine to execute
	p.mu.RLock() // avoid data race with first poller goroutine
	require.Len(t, p.waiters, 1, "expected one poller waiting for an item")
	p.mu.RUnlock()

	wg.Add(1)
	go func() {
		wg.Done()
		sng, err := p.poll(0)
		require.NoError(t, err, "failed to poll item from queue")
		assert.Equal(t, passToSecondPoller, sng.title)
		wg.Done()
	}()
	wg.Wait()    // give a chance for the second poller goroutine to execute
	p.mu.RLock() // avoid data race with second poller goroutine
	require.Len(t, p.waiters, 2, "expected two pollers waiting for an item")
	p.mu.RUnlock()

	// queue two songs and wait for poller goroutines to receive them
	wg.Add(2)
	err = p.Enqueue("", passToFirstPoller, nil)
	require.NoError(t, err, "failed to queue into empty queue with two pollers")
	require.Empty(t, p.queue, "expected to pass item directly to poller")

	err = p.Enqueue("", passToSecondPoller, nil)
	require.NoError(t, err, "failed to queue into empty queue with two pollers")
	require.Empty(t, p.queue, "expected to pass item directly to poller")
	wg.Wait()

	// poller that should time out
	wg.Add(1)
	go func() {
		_, err := p.poll(1)
		assert.Equal(t, errPollTimeout, err)
		wg.Done()
	}()
	wg.Wait()

	err = p.Enqueue("", ignoreDeadPoller, nil)
	require.NoError(t, err, "failed to queue into empty queue with one timed out poller")
	require.Len(t, p.queue, 1, "expected to pass song into queue instead of timed out poller")

	sng, err = p.poll(1)
	require.NoError(t, err, "failed to poll item from non-empty queue with one timed out poller")

	assert.Equal(t, ignoreDeadPoller, sng.title)
}

func TestClose(t *testing.T) {
	t.Parallel()
	p := New(&nopDevice{})
	require.NotNil(t, p)

	// open pollers should die when player is closed
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		wg.Done()
		_, err := p.poll(0)
		assert.Equal(t, ErrClosed, err, "open pollers should fail once the player is closed")
		wg.Done()
	}()
	wg.Wait()

	wg.Add(1)
	p.Close()
	// wait for poller routine to close
	wg.Wait()

	// attempts to enqueue into closed player should fail
	err := p.Enqueue("", "fail to queue into closed player", nil)
	assert.Equal(t, ErrClosed, err, "enqueue should fail on a closed player")

	// close as many times as you want
	assert.Equal(t, ErrClosed, p.Close())
	assert.Equal(t, ErrClosed, p.Close())
	assert.Equal(t, ErrClosed, p.Close())

	// close should empty the queue and skip the currently playing song
	p = New(&nopDevice{}, QueueLength(1))
	require.NotNil(t, p)
	require.Empty(t, p.queue)

	wg.Add(1)
	err = p.Enqueue("", "pause and block playback", nopSongOpener,
		PreEncoded(),
		OnStart(func() {
			p.Pause()
		}),
		OnPause(func(_ time.Duration) {
			wg.Done()
		}),
		OnEnd(func(_ time.Duration, err error) {
			endErr := errors.Cause(err)
			assert.Equal(t, ErrClosed, endErr, "close should skip the currently playing song, even if paused")
		}),
	)
	require.NoError(t, err)
	wg.Wait()

	err = p.Enqueue("", "", nil)
	require.NoError(t, err)
	require.Len(t, p.queue, 1)

	p.Close()

	assert.Empty(t, p.queue, "close should empty the queue")
}

func TestPlaylistAndClear(t *testing.T) {
	songs := []string{
		"hello",
		"world",
		"apple",
		"banana",
		"grape",
		"orange",
		"foo",
		"bar",
		"baz",
		"qux",
	}
	t.Parallel()
	p := New(&nopDevice{}, QueueLength(len(songs)))
	require.NotNil(t, p)
	defer p.Close()

	// queue a song and immediately pause it to freeze playback and prevent queue from being consumed
	// wait for it to be paused
	songEnded := false
	var wg sync.WaitGroup
	wg.Add(1)
	err := p.Enqueue("", "", nopSongOpener,
		PreEncoded(),
		OnStart(func() {
			p.Pause()
		}),
		OnPause(func(_ time.Duration) {
			wg.Done()
		}),
		OnEnd(func(_ time.Duration, err error) {
			songEnded = true
		}))
	require.NoError(t, err)
	wg.Wait()

	require.Empty(t, p.queue)
	for idx, title := range songs {
		err := p.Enqueue("", title, nil)
		require.NoErrorf(t, err, "failed to queue song %v:%v", idx, title)
		assert.Equal(t, songs[0:idx+1], p.Playlist())
	}

	require.Len(t, p.queue, len(songs))
	for idx, title := range songs {
		sng, err := p.poll(1)
		require.NoErrorf(t, err, "failed to poll song %v:%v", idx, title)
		assert.Equal(t, title, sng.title)
		assert.Equal(t, songs[idx+1:], p.Playlist())
	}

	require.Empty(t, p.queue)
	for idx, title := range songs {
		err := p.Enqueue("", title, nil)
		require.NoErrorf(t, err, "failed to queue song %v:%v", idx, title)
		assert.Equal(t, songs[0:idx+1], p.Playlist())
	}

	require.Len(t, p.queue, len(songs))
	p.Clear()
	assert.Empty(t, p.queue)
	assert.Empty(t, p.Playlist())
	assert.False(t, songEnded)
}
