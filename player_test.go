package player

import (
	"io"
	"io/ioutil"
	"strings"
	"sync"
	"testing"
)

type testOpener struct{}
type nopCloser struct{ io.Writer }

func (w nopCloser) Close() error { return nil }
func (o *testOpener) Open(x string) (io.WriteCloser, error) {
	return nopCloser{ioutil.Discard}, nil
}

func TestNewPlayer(t *testing.T) {
	calledIdle := false
	p := New(&testOpener{}, IdleFunc(func() { calledIdle = true }, 1))
	if p == nil {
		t.Fatal("player is nil")
	}
	defer p.Close()
	if !calledIdle {
		t.Error("new player did not call idle")
	}
}

var nopSongOpener = func() (io.ReadCloser, error) {
	return ioutil.NopCloser(strings.NewReader("hello world")), nil
}

func TestEnqueue(t *testing.T) {
	p := New(&testOpener{}, QueueLength(1))
	if p == nil {
		t.Fatal("player is nil")
	}
	defer p.Close()

	// signal when the song is playing and keep it there
	var wg sync.WaitGroup
	wg.Add(1)
	err := p.Enqueue("x", "play item", nopSongOpener, OnStart(func() { wg.Done(); p.Pause() }))
	if err != nil {
		t.Fatal("failed to queue a song when queue is empty:", err)
	}
	wg.Wait()

	if lst := p.Playlist(); len(lst) != 0 {
		t.Fatal("expected queue to be empty, contains:", lst)
	}

	pollTitle := "queued item"
	err = p.Enqueue("x", pollTitle, nopSongOpener)
	if err != nil {
		t.Fatal("failed to queue a song when queue is empty:", err)
	}

	if lst := p.Playlist(); len(lst) != 1 {
		t.Fatal("expected queue to have one item, contains:", lst)
	}

	if err := p.Enqueue("x", "fail to queue item", nopSongOpener); err != ErrFull {
		t.Fatal("managed to queue a song when queue is full:", err)
	}

	sng, err := p.poll(1)
	if err != nil {
		t.Fatal("failed to poll item from non-empty queue:", err)
	}
	if sng.title != pollTitle {
		t.Errorf("expected polled item to have title %v, actual %v", pollTitle, sng.title)
	}
}
