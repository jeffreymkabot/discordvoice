package discordvoice

import (
	"errors"
	"sync"
	"time"

	"github.com/Workiva/go-datastructures/queue"
	"github.com/bwmarrin/discordgo"
)

type control uint8

const (
	nop control = iota
	skip
	pause
)

// ErrFull is emitted when the Payload queue is full
var ErrFull = errors.New("Full send buffer")

// DefaultConfig is the config that is used when no PlayerOptions are passed to Connect
var DefaultConfig = PlayerConfig{
	QueueLength: 100,
	SendTimeout: 1000,
	IdleTimeout: 300,
}

// PlayerConfig sets some behaviors of the Player
type PlayerConfig struct {
	QueueLength int `toml:"queue_length"`
	SendTimeout int `toml:"send_timeout"`
	IdleTimeout int `toml:"afk_timeout"`
}

// PlayerOption
type PlayerOption func(*PlayerConfig)

// QueueLength is the maximum number of Payloads that will be allowed in the queue
func QueueLength(n int) PlayerOption {
	return func(cfg *PlayerConfig) {
		if n > 0 {
			cfg.QueueLength = n
		}
	}
}

// SendTimeout is number of milliseconds the player will wait to attempt to send an audio frame
// before moving onto the next Payload
func SendTimeout(n int) PlayerOption {
	return func(cfg *PlayerConfig) {
		if n > 0 {
			cfg.SendTimeout = n
		}
	}
}

// IdleTimeout is the number of milliseconds the player will wait for another Payload before joining the idle channel
func IdleTimeout(n int) PlayerOption {
	return func(cfg *PlayerConfig) {
		if n > 0 {
			cfg.IdleTimeout = n
		}
	}
}

// Player
type Player struct {
	// mutex to prevent concurrent invocations of enqueue from getting around QueueLength
	mu sync.Mutex
	// can't use a ringbuffer since it's bugged and uses all the cpu
	queue   *queue.Queue
	control chan control
	cfg     *PlayerConfig
}

// Connect launches a Player that dispatches voice to a discord guild
// Since discord allows only one voice connection per guild, you should call Player.Quit before calling Connect again for the same guild
func Connect(s *discordgo.Session, guildID string, idleChannelID string, opts ...PlayerOption) *Player {
	cfg := DefaultConfig
	for _, opt := range opts {
		opt(&cfg)
	}
	// cfg no longer changes after applying any options
	// don't need to use mutex to safely access its properties

	queue := queue.New(int64(cfg.QueueLength))
	ctrl := make(chan control)

	player := &Player{
		queue:   queue,
		control: ctrl,
		cfg:     &cfg,
	}

	go sender(player, s, guildID, idleChannelID)

	return player
}

// Enqueue puts an item at the end of the queue
func (play *Player) Enqueue(channelID string, url string, opts ...SongOption) error {
	play.mu.Lock()
	if play.queue.Len() >= int64(play.cfg.QueueLength) {
		play.mu.Unlock()
		return ErrFull
	}

	s := &song{
		channelID:  channelID,
		url:        url,
		title:      url,
		onStart:    func() {},
		onEnd:      func(time.Duration, error) {},
		onProgress: func(time.Duration) {},
		onPause:    func(time.Duration) {},
		onResume:   func(time.Duration) {},
	}

	for _, opt := range opts {
		opt(s)
	}

	err := play.queue.Put(s)
	play.mu.Unlock()

	return err
}

// Length returns the number of items in the queue
func (play *Player) Length() int {
	return int(play.queue.Len())
}

// Next returns the title of the next item in the queue
func (play *Player) Next() string {
	if item, err := play.queue.Peek(); err != nil {
		if s, ok := item.(*song); ok {
			return s.title
		}
	}
	return ""
}

// Skip moves to the next item in the queue when an item is playing or is paused
// Skip does nothing when there is no item in the queue or the player is already processing a control
func (play *Player) Skip() error {
	select {
	case play.control <- skip:
	case <-time.After(1 * time.Second):
		return ErrFull
	}
	return nil
}

// Clear removes all items from the queue
// Clear does not skip the current song
func (play *Player) Clear() error {
	// doesn't need to use player lock because queue has its own internal lock
	// i.e. play.queue.Put will block until this is done
	items, err := play.queue.TakeUntil(func(i interface{}) bool { return true })
	for _, item := range items {
		if s, ok := item.(*song); ok {
			s.onEnd(0, errors.New("cleared"))
		}
	}
	return err
}

// Pause stops the currently playing item or resumes the currently paused item
// Pause does nothing when there is no item in the queue or the player is already processing a control
func (play *Player) Pause() error {
	select {
	case play.control <- pause:
	case <-time.After(1 * time.Second):
		return ErrFull
	}
	return nil
}

// Quit closes the player
// You should call quit before calling connect again for the same guild
func (play *Player) Quit() {
	if play.queue.Disposed() {
		return
	}

	items := play.queue.Dispose()
	for _, item := range items {
		if s, ok := item.(*song); ok {
			s.onEnd(0, errors.New("quit"))
		}
	}
	//
	close(play.control)
}
