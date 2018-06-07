// Package player provides controllable queued playback of streams to an audio device.
package player

import (
	"io"
	"sync"
	"time"

	"github.com/pkg/errors"
)

// Version follows semantic versioning.
const Version = "0.4.1"

// Player errors
var (
	ErrFull    = errors.New("queue is full")
	ErrClosed  = errors.New("player is closed")
	ErrCleared = errors.New("cleared")
	ErrSkipped = errors.New("skipped")
)

var (
	errPollTimeout = errors.New("poll timeout")
)

// Player provides controllable playback to the provided audio device via a queue.
// Player is safe to use in multiple goroutines.
type Player struct {
	cfg  *config
	quit chan struct{}
	wg   sync.WaitGroup

	// device resource possibly opened by playback goroutine
	writer io.Writer

	mu      sync.RWMutex
	queue   []*songItem
	waiters []waiter
	ctrl    chan control
}

// DeviceOpenerFunc provides the writer for playback.
// If the writer also implements io.Closer it will be closed when the player is closed.
type DeviceOpenerFunc func() (io.Writer, error)

// SongOpenerFunc opens an audio stream.
// If the reader also implements io.Closer it will be closed after playback.
type SongOpenerFunc func() (io.Reader, error)

type EncodeFunc func(io.Reader) (Source, error)

type Source interface {
	ReadFrame() ([]byte, error)
	FrameDuration() time.Duration
}

type SourceCloser interface {
	Source
	io.Closer
}

type songItem struct {
	openSrc SongOpenerFunc
	openDst DeviceOpenerFunc
	title   string

	encoder  EncodeFunc
	loudness float64
	filters  string
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

// New creates a Player.
// Be sure to call Player.Close to clean up any resources.
func New(opts ...Option) *Player {
	cfg := config{Idle: func() {}}
	for _, opt := range opts {
		opt(&cfg)
	}

	player := &Player{
		cfg:  &cfg,
		quit: make(chan struct{}),
		// buffered so Skip()/Pause() do not wait for if playback is busy reading/writing
		ctrl: make(chan control, 1),
	}

	player.cfg.Idle()
	go player.playback()

	return player
}

// Enqueue puts an item at the end of the queue.
func (p *Player) Enqueue(title string, openSrc SongOpenerFunc, openDst DeviceOpenerFunc, opts ...SongOption) error {
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
		openSrc: openSrc,
		openDst: openDst,
		title:   title,
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
	select {
	case <-p.quit:
		return nil, ErrClosed
	default:
	}

	var deadline <-chan time.Time
	if timeout > 0 {
		deadline = time.NewTimer(timeout).C
	}

	p.mu.Lock()
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
		close(me.dead)
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
	p.clear(ErrCleared)
}

func (p *Player) clear(reason error) {
	for _, s := range p.queue {
		s.onEnd(0, reason)
	}
	p.queue = nil
}

// Skip the currently playing or paused item.
func (p *Player) Skip() {
	// ctrl channel is buffered to 1
	select {
	case p.ctrl <- skip:
	default:
	}
}

// Pause the currently playing item or resume the currently paused item.
func (p *Player) Pause() {
	// ctrl channel is buffered to 1
	select {
	case p.ctrl <- pause:
	default:
	}
}

// Close releases the resources for the player and all queued items.
// Close will block until all OnEnd callbacks have returned.
// You should call Close before opening another Player targetting the same resources.
func (p *Player) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	select {
	case <-p.quit:
		return ErrClosed
	default:
	}

	close(p.quit)
	// clear calls onEnd callbacks of queued songs
	p.clear(ErrClosed)
	// wait for onEnd callback of currently playing song
	p.wg.Wait()
	return nil
}

// send signals to the currently playing item
type control byte

const (
	nop control = iota
	skip
	pause
)
