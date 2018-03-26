package player

import (
	"io"
	"io/ioutil"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pkg/errors"
)

type nopWriterOpener struct{}

func (o *nopWriterOpener) Open(x string) (io.WriteCloser, error) {
	return nopWriter{ioutil.Discard}, nil
}

type nopWriter struct {
	io.Writer
}

func (w nopWriter) Close() error { return nil }

var nopSongOpener = func() (io.ReadCloser, error) {
	return ioutil.NopCloser(strings.NewReader("hello world")), nil
}

func TestNewPlayer(t *testing.T) {
	t.Parallel()
	calledIdle := false
	p := New(&nopWriterOpener{}, IdleFunc(func() { calledIdle = true }, 1))
	if p == nil {
		t.Fatal("player is nil")
	}
	defer p.Close()
	if !calledIdle {
		t.Error("new player did not call idle")
	}
	if lst := p.Playlist(); len(lst) != 0 {
		t.Error("playlist is not empty")
	}
}

func TestEnqueuePoll(t *testing.T) {
	t.Parallel()
	p := New(&nopWriterOpener{}, QueueLength(1))
	if p == nil {
		t.Fatal("player is nil")
	}
	defer p.Close()

	songs := []string{
		"pause and block playback",
		"occupy queue",
		"fail to queue into full queue",
		"pass directly to first poller",
		"pass directly to second poller",
	}

	// queue a song and immediately pause it to freeze playback and prevent queue from being consumed
	// wait for it to be paused
	var wg sync.WaitGroup
	wg.Add(1)
	err := p.Enqueue("", songs[0], nopSongOpener, OnStart(func() { p.Pause(); wg.Done() }))
	if err != nil {
		t.Fatal("failed to queue a song when queue is empty:", err)
	}
	wg.Wait()
	if lst := p.Playlist(); len(lst) != 0 {
		t.Fatal("expected playlist to be empty, contains:", lst)
	}

	// queue a song
	if err := p.Enqueue("", songs[1], nil); err != nil {
		t.Fatal("failed to queue a song when queue is empty:", err)
	}
	if lst := p.Playlist(); len(lst) != 1 {
		t.Fatal("expected playlist to have one item, contains:", lst)
	}
	if lst := p.Playlist(); !reflect.DeepEqual(lst, songs[1:2]) {
		t.Errorf("expected playlist to be %v, actual %v", songs[1:2], lst)
	}

	// queue should be full
	if err := p.Enqueue("", songs[2], nil); err != ErrFull {
		t.Fatalf("expected to fail enqueue with error %v, actual %v", ErrFull, err)
	}

	// remove the queued song
	sng, err := p.poll(1)
	if err != nil {
		t.Fatal("failed to poll item from non-empty queue:", err)
	}
	if sng.title != songs[1] {
		t.Errorf("expected polled item to have title %v, actual %v", songs[1], sng.title)
	}
	if lst := p.Playlist(); len(lst) != 0 {
		t.Fatal("expected playlist to be empty, actual:", lst)
	}

	// set up two routines that poll indefinitely for queued songs
	// wait for poller goroutines to begin execution
	// make sure poller goroutines execute in order
	wg.Add(1)
	go func() {
		wg.Done()
		sng, err := p.poll(0)
		if err != nil {
			t.Fatal("failed to poll item from queue:", err)
		}
		if sng.title != songs[3] {
			t.Errorf("expected polled item to have title %v, actual %v", songs[3], sng.title)
		}
		wg.Done()
	}()
	wg.Wait()
	wg.Add(1)
	go func() {
		wg.Done()
		sng, err := p.poll(0)
		if err != nil {
			t.Fatal("failed to poll item from queue:", err)
		}
		if sng.title != songs[4] {
			t.Errorf("expected polled item to have title %v, actual %v", songs[4], sng.title)
		}
		wg.Done()
	}()
	wg.Wait()

	// queue two songs and wait for poller goroutines to receive them
	wg.Add(2)
	if err := p.Enqueue("", songs[3], nil); err != nil {
		t.Fatal("failed to pass item to non-empty queue with two pollers:", err)
	}
	if err := p.Enqueue("", songs[4], nil); err != nil {
		t.Fatal("failed to pass item to non-empty queue with two pollers:", err)
	}
	wg.Wait()

	// poller that should time out
	wg.Add(1)
	go func() {
		if _, err := p.poll(1); err != errPollTimeout {
			t.Errorf("expected to fail poll with error %v, actual %v", errPollTimeout, err)
		}
		wg.Done()
	}()
	wg.Wait()
}

func TestClose(t *testing.T) {
	t.Parallel()
	p := New(&nopWriterOpener{}, QueueLength(1))
	if p == nil {
		t.Fatal("player is nil")
	}

	// open pollers should fail when player is closed
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		wg.Done()
		if _, err := p.poll(0); err != ErrClosed {
			t.Errorf("expected to fail poll with error %v, actual %v", ErrClosed, err)
		}
		wg.Done()
	}()
	wg.Wait()

	wg.Add(1)
	p.Close()
	wg.Wait()

	// attempts to enqueue into closed player should fail
	if err := p.Enqueue("", "fail to queue into closed player", nil); err != ErrClosed {
		t.Errorf("expected to fail enqueue with error %v, actual %v", ErrClosed, err)
	}

	// close as many times as you want
	p.Close()
	p.Close()
	p.Close()
	if err := p.Close(); err != ErrClosed {
		t.Errorf("expcted close to fail with error %v, actual %v", ErrClosed, err)
	}

	// close should empty the playlist and skip the currently playing song
	p = New(&nopWriterOpener{}, QueueLength(1))
	if p == nil {
		t.Fatal("player is nil")
	}
	if lst := p.Playlist(); len(lst) != 0 {
		t.Fatal("playlist is not empty")
	}

	wg.Add(1)
	var endErr error
	err := p.Enqueue("", "", nopSongOpener,
		OnStart(func() {
			p.Pause()
			wg.Done()
		}),
		OnEnd(func(e time.Duration, err error) {
			endErr = errors.Cause(err)
		}),
	)
	if err != nil {
		t.Fatal("failed to queue a song when queue is empty:", err)
	}
	wg.Wait()

	if err := p.Enqueue("", "", nil); err != nil {
		t.Fatal("failed to queue a song when queue is empty:", err)
	}
	if lst := p.Playlist(); len(lst) != 1 {
		t.Fatal("expected playlist to have one item, contains:", lst)
	}

	p.Close()

	if lst := p.Playlist(); len(lst) != 0 {
		t.Error("playlist is not empty, contains:", lst)
	}
	if endErr != ErrClosed {
		t.Errorf("expected song to end with %v, actual %v", ErrClosed, endErr)
	}
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
	testFn := func(bounded bool) func(t *testing.T) {
		return func(t *testing.T) {
			t.Parallel()
			var p *Player
			if bounded {
				p = New(&nopWriterOpener{}, QueueLength(len(songs)))
			} else {
				p = New(&nopWriterOpener{})
			}
			if p == nil {
				t.Fatal("player is nil")
			}
			defer p.Close()

			// queue a song and immediately pause it to freeze playback and prevent queue from being consumed
			// wait for it to be paused
			var wg sync.WaitGroup
			wg.Add(1)
			err := p.Enqueue("", "", nopSongOpener, OnStart(func() { p.Pause(); wg.Done() }))
			if err != nil {
				t.Fatal("failed to queue a song when queue is empty", err)
			}
			wg.Wait()

			for idx, sng := range songs {
				if err := p.Enqueue("", sng, nil); err != nil {
					t.Fatal("failed to queue song", idx, err)
				}
				if lst := p.Playlist(); !reflect.DeepEqual(songs[0:idx+1], lst) {
					t.Errorf("expected playlist to be %v, actual %v", songs[0:idx+1], lst)
				}
			}

			p.Clear()
			if lst := p.Playlist(); len(lst) != 0 {
				t.Error("expected playlist to be empty, actual", lst)
			}
		}
	}
	t.Run("bounded queue", testFn(true))
	t.Run("unbounded queue", testFn(false))
}

func TestCallbacks(t *testing.T) {
	t.Parallel()
	p := New(&nopWriterOpener{}, QueueLength(1))
	if p == nil {
		t.Fatal("player is nil")
	}
	defer p.Close()

	var waitForPause sync.WaitGroup
	var waitForEnd sync.WaitGroup
	waitForPause.Add(1)
	waitForEnd.Add(1)

	var calledOnStart, calledOnPause, calledOnResume, calledOnProgress, calledOnEnd bool
	var pauseTime time.Duration
	var resumeTime time.Duration
	var endErr error
	err := p.Enqueue("", "", nopSongOpener, PreEncoded(),
		OnStart(func() {
			calledOnStart = true
			p.Pause()
		}),
		OnPause(func(elapsed time.Duration) {
			calledOnPause = true
			// song should have paused itself in OnStart
			pauseTime = elapsed
			waitForPause.Done()

		}),
		OnResume(func(elapsed time.Duration) {
			calledOnResume = true
			resumeTime = elapsed

		}),
		OnProgress(func(elapsed time.Duration, times []time.Time) {
			calledOnProgress = true
		}, 0),
		OnEnd(func(elapsed time.Duration, err error) {
			calledOnEnd = true
			endErr = errors.Cause(err)
			waitForEnd.Done()
		}),
	)
	if err != nil {
		t.Fatal("failed to queue song", err)
	}
	waitForPause.Wait()

	p.Pause()
	waitForEnd.Wait()

	if !calledOnStart {
		t.Error("failed to call OnStart callback")
	}
	if !calledOnPause {
		t.Error("failed to call OnPause callback")
	}
	if !calledOnResume {
		t.Error("failed to call OnResume callback")
	}
	if calledOnProgress {
		t.Error("called OnProgress callback with invalid progress interval")
	}
	if !calledOnEnd {
		t.Error("failed to call OnEnd callback")
	}
	if pauseTime > 0 {
		t.Error("expected song to be paused at 0 elapsed, actual", pauseTime)
	}
	if resumeTime != pauseTime {
		t.Errorf("expected song to have no progress between pause and resume, paused at %v, resumed at %v", pauseTime, resumeTime)
	}
	if endErr != io.EOF && endErr != io.ErrUnexpectedEOF {
		t.Error("expected song to read/write until EOF, actual", endErr)
	}
}

func TestSkip(t *testing.T) {
	t.Parallel()
	p := New(&nopWriterOpener{}, QueueLength(1))
	if p == nil {
		t.Fatal("player is nil")
	}
	defer p.Close()

	var endErr error
	var waitForPause sync.WaitGroup
	var waitForEnd sync.WaitGroup
	waitForPause.Add(1)
	waitForEnd.Add(1)
	err := p.Enqueue("", "", nopSongOpener,
		OnStart(func() {
			p.Pause()
		}),
		OnPause(func(e time.Duration) {
			waitForPause.Done()
		}),
		OnEnd(func(e time.Duration, err error) {
			endErr = errors.Cause(err)
			waitForEnd.Done()
		}),
	)
	if err != nil {
		t.Fatal("failed to queue a song when queue is empty:", err)
	}
	waitForPause.Wait()
	// TODO test skipping a song that is not paused
	p.Skip()
	waitForEnd.Wait()
	if endErr != errSkipped {
		t.Errorf("expected song to end with %v, actual %v", errSkipped, endErr)
	}
}
