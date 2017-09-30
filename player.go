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
	CanBroadcastStatus: false,
}

// PlayerConfig sets some behaviors of the Player
type PlayerConfig struct {
	QueueLength        int `toml:"queue_length"`
	SendTimeout        int `toml:"send_timeout"`
	IdleTimeout        int `toml:"afk_timeout"`
	CanBroadcastStatus bool `toml:"broadcast_status"`
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

// CanBroadcastStatus
func CanBroadcastStatus(b bool) PlayerOption {
	return func(cfg *PlayerConfig) {
		cfg.CanBroadcastStatus = b
	}
}

type Player struct {
	// mutex to prevent concurrent attempts to enqueue to get around QueueLength
	mu   sync.Mutex
	quit chan struct{}
	// can't use a ringbuffer since it's bugged and uses all the cpu
	queue   *queue.Queue
	control chan control
	cfg     *PlayerConfig
}

// Enqueue puts an item at the end of the queue
func (play *Player) Enqueue(p *Payload) error {
	play.mu.Lock()
	defer play.mu.Unlock()
	if play.queue.Len() >= int64(play.cfg.QueueLength) {
		return ErrFull
	}
	return play.queue.Put(p)
}

// Length returns the number of items in the queue
func (play *Player) Length() int {
	return int(play.queue.Len())
}

// Next returns the name of the next item in the queue
func (play *Player) Next() string {
	if item, err := play.queue.Peek(); err != nil {
		if p, ok := item.(*Payload); ok {
			return p.Name
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

// Connect launches a Player that dispatches voice to a discord guild
// Since discord allows only one voice connection per guild, you should call Player.Quit before calling connect again for the same guild
func Connect(s *discordgo.Session, guildID string, idleChannelID string, opts ...PlayerOption) *Player {
	cfg := DefaultConfig
	for _, opt := range opts {
		opt(&cfg)
	}

	quit := make(chan struct{})
	queue := queue.New(int64(cfg.QueueLength))
	go func() {
		<-quit
		queue.Dispose()
	}()
	ctrl := make(chan control, 1)

	send := &Player{
		quit:    quit,
		queue:   queue,
		control: ctrl,
		cfg:     &cfg,
	}

	go payloadSender(send, s, guildID, idleChannelID, cfg.CanBroadcastStatus, cfg.SendTimeout, cfg.IdleTimeout)

	return send
}
