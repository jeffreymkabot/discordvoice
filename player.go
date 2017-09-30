package discordvoice

import (
	"errors"
	"sync"

	"github.com/Workiva/go-datastructures/queue"
	"github.com/bwmarrin/discordgo"
)

type control uint8

const (
	nop control = iota
	skip
	pause
)

// ErrFull is emitted when the Payload queue or control queue is full
var ErrFull = errors.New("Full send buffer")

// DefaultConfig is the config that is used when no PlayerOptions are passed to Connect
var DefaultConfig = PlayerConfig{
	QueueLength:        100,
	SendTimeout:        1000,
	IdleTimeout:        300,
}

// PlayerConfig sets some behaviors of the Player
type PlayerConfig struct {
	QueueLength        int  `toml:"queue_length"`
	SendTimeout        int  `toml:"send_timeout"`
	IdleTimeout        int  `toml:"afk_timeout"`
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
	// mutex to prevent concurrent attempts to enqueue to get around QueueLength
	mu   sync.Mutex
	quit chan struct{}
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

	quit := make(chan struct{})
	queue := queue.New(int64(cfg.QueueLength))
	go func() {
		<-quit
		items := queue.Dispose()
		for _, item := range items {
			if p, ok := item.(*song); ok {
				// close p.status to free any listeners
				// this won't panic because close is elsehwere only called on status chans of payloads taken out of queue
				close(p.status)
			}
		}
	}()
	ctrl := make(chan control, 1)

	player := &Player{
		quit:    quit,
		queue:   queue,
		control: ctrl,
		cfg:     &cfg,
	}

	go sender(player, s, guildID, idleChannelID)

	return player
}

// Enqueue puts an item at the end of the queue
func (play *Player) Enqueue(channelID string, url string, opts ...SongOption) (<-chan SongStatus, error) {
	play.mu.Lock()
	if play.queue.Len() >= int64(play.cfg.QueueLength) {
		play.mu.Unlock()
		return nil, ErrFull
	}

	status := make(chan SongStatus, 1)

	p := &song{
		channelID: channelID,
		url:       url,
		title:     url,
		status:    status,
	}

	for _, opt := range opts {
		opt(p)
	}

	err := play.queue.Put(p)
	play.mu.Unlock()

	if err != nil {
		close(status)
		return nil, err
	}

	return status, nil
}

// Length returns the number of items in the queue
func (play *Player) Length() int {
	return int(play.queue.Len())
}

// Next returns the title of the next item in the queue
func (play *Player) Next() string {
	if item, err := play.queue.Peek(); err != nil {
		if p, ok := item.(*song); ok {
			return p.title
		}
	}
	return ""
}

// Skip moves to the next item in the queue when an item is playing or is paused
// Skip does nothing when there is no item in the queue or the player is already processing a control
func (play *Player) Skip() error {
	select {
	case play.control <- skip:
	default:
		return ErrFull
	}
	return nil
}

// Clear removes all items from the queue
// Clear does not skip the current song
func (play *Player) Clear() error {
	// doesn't need a lock because queue has its own internal lock
	items, err := play.queue.TakeUntil(func(i interface{}) bool { return true })
	for _, item := range items {
		if p, ok := item.(*song); ok {
			// close p.status to free any listeners
			// this won't panic because close is elsehwere only called on status chans of payloads taken out of queue
			// and every take uses the queue's internal lock
			close(p.status)
		}
	}
	return err
}

// Pause stops the currently playing item or resumes the currently paused item
// Pause does nothing when there is no item in the queue or the player is already processing a control
func (play *Player) Pause() error {
	select {
	case play.control <- pause:
	default:
		return ErrFull
	}
	return nil
}

// Quit closes the player
// You should call quit before calling connect again for the same guild
func (play *Player) Quit() {
	select {
	case <-play.quit:
		// already closed, don't close a closed channel
		return
	default:
		close(play.quit)
	}
}
