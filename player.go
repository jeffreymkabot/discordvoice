package player

import (
	"io"
	"sync"
	"time"

	"github.com/pkg/errors"
)

const Version = "0.4.1"

// Player errors
var (
	ErrFull   = errors.New("queue is full")
	ErrClosed = errors.New("player is closed")
)

var (
	errPollTimeout = errors.New("poll timeout")
	errCleared     = errors.New("cleared")
)

// Player
type Player struct {
	cfg  *PlayerConfig
	quit chan struct{}
	wg   sync.WaitGroup

	mu      sync.RWMutex
	queue   []*songItem
	waiters []waiter
	ctrl    chan control
}

type songItem struct {
	channelID string
	open      SongOpenFunc
	title     string

	preencoded bool
	loudness   float64
	filters    string
	callbacks
}

type callbacks struct {
	duration         time.Duration
	onStart          func()
	onPause          func(elapsed time.Duration)
	onResume         func(elapsed time.Duration)
	progressInterval time.Duration
	onProgress       func(elapsed time.Duration, frameTimes []time.Duration)
	onEnd            func(elapsed time.Duration, err error)
}

type waiter struct {
	dead  chan struct{}
	input chan *songItem
}

type WriterOpener interface {
	Open(channelID string) (io.WriteCloser, error)
}

func New(opener WriterOpener, opts ...PlayerOption) *Player {
	cfg := PlayerConfig{Idle: func() {}}
	for _, opt := range opts {
		opt(&cfg)
	}

	player := &Player{
		cfg:  &cfg,
		quit: make(chan struct{}),
		// buffered so Skip()/Pause() do not wait if busy
		ctrl: make(chan control, 1),
	}

	player.cfg.Idle()
	go playback(player, opener)

	return player
}

type SongOpenFunc func() (io.ReadCloser, error)

// Enqueue puts an item at the end of the queue.
func (p *Player) Enqueue(channelID string, title string, open SongOpenFunc, opts ...SongOption) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	select {
	case <-p.quit:
		return ErrClosed
	default:
	}

	if p.cfg.QueueLength > 0 && len(p.queue) >= p.cfg.QueueLength {
		return ErrFull
	}

	song := &songItem{
		channelID: channelID,
		open:      open,
		title:     title,
		callbacks: callbacks{
			onStart:    func() {},
			onEnd:      func(time.Duration, error) {},
			onProgress: func(time.Duration, []time.Duration) {},
			onPause:    func(time.Duration) {},
			onResume:   func(time.Duration) {},
		},
	}

	for _, opt := range opts {
		opt(song)
	}

	// bypass queue and submit song straight to the first poller still waiting for a song
	for len(p.waiters) > 0 {
		waiter := p.waiters[0]
		p.waiters = p.waiters[1:]
		select {
		case <-p.quit:
			return ErrClosed
		case waiter.input <- song:
			return nil
		case <-waiter.dead:
			// waiter stopped waiting, try the next one
		}
	}

	p.queue = append(p.queue, song)
	return nil
}

// poll blocks until an item is queued, player is closed, or timeout has passed if timeout > 0
func (p *Player) poll(timeout time.Duration) (*songItem, error) {
	p.mu.Lock()
	select {
	case <-p.quit:
		p.mu.Unlock()
		return nil, ErrClosed
	default:
	}

	var deadline <-chan time.Time
	if timeout > 0 {
		deadline = time.NewTimer(timeout).C
	}

	if len(p.queue) > 0 {
		song := p.queue[0]
		p.queue = p.queue[1:]
		p.mu.Unlock()
		return song, nil
	}

	// add me to the list of waiters and wait for a song
	// input channel must not be buffered so the closed dead channel takes priority in Enqueue's select statement
	// otherwise Enqueue would randomly pass to dead pollers :(
	me := waiter{
		input: make(chan *songItem),
		dead:  make(chan struct{}),
	}
	p.waiters = append(p.waiters, me)
	p.mu.Unlock()

	select {
	case <-p.quit:
		return nil, ErrClosed
	case <-deadline:
		// make sure enqueue does not consider me eligible anymore
		close(me.dead)
		return nil, errPollTimeout
	case song := <-me.input:
		return song, nil
	}
}

// Playlist returns the titles of items in the queue.
func (p *Player) Playlist() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	titles := make([]string, len(p.queue))
	for i, song := range p.queue {
		titles[i] = song.title
	}
	return titles
}

// Clear removes all queued items.
// Clear does not skip the currently playing item.
func (p *Player) Clear() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.clear(errCleared)
}

func (play *Player) clear(reason error) {
	for _, s := range play.queue {
		s.onEnd(0, reason)
	}
	play.queue = nil
}

// Skip the currently playing or paused item.
func (play *Player) Skip() {
	// ctrl channel is buffered to 1
	select {
	case play.ctrl <- skip:
	default:
	}
}

// Pause the currently playing item or resume the currently paused item.
func (play *Player) Pause() {
	// ctrl channel is buffered to 1
	select {
	case play.ctrl <- pause:
	default:
	}
}

// Close releases the resources for the player and all queued items.
// Close will block until all OnEnd callbacks have returned.
// You should call Close before opening another Player targetting the same resources.
func (play *Player) Close() error {
	play.mu.Lock()
	defer play.mu.Unlock()
	select {
	case <-play.quit:
		return ErrClosed
	default:
	}

	close(play.quit)
	// clear calls onEnd callbacks of queued songs
	play.clear(ErrClosed)
	// wait for onEnd callback of currently playing song
	play.wg.Wait()
	return nil
}

// send signals to the currently playing item
type control byte

const (
	nop control = iota
	skip
	pause
)
