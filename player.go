package discordvoice

import (
	"errors"
	"sync"
	"time"

	"github.com/Workiva/go-datastructures/queue"
	"github.com/bwmarrin/discordgo"
)

type control byte

const (
	nop control = iota
	skip
	pause
)

// ErrFull is emitted when the Player queue is full.
var ErrFull = errors.New("Full send buffer")

// ErrInvalidVoiceChannel is emitted when a song is queued without a real voice channel.
var ErrInvalidVoiceChannel = errors.New("Invalid voice channel")

// DefaultConfig is the config that is used when no PlayerOptions are passed to Connect.
var DefaultConfig = PlayerConfig{
	QueueLength: 100,
	SendTimeout: 1000,
	IdleTimeout: 300,
}

// PlayerConfig sets some behaviors of the Player.
type PlayerConfig struct {
	QueueLength int `toml:"queue_length"`
	SendTimeout int `toml:"send_timeout"`
	IdleTimeout int `toml:"afk_timeout"`
}

// PlayerOption
type PlayerOption func(*PlayerConfig)

// QueueLength is the maximum number of Payloads that will be allowed in the queue.
func QueueLength(n int) PlayerOption {
	return func(cfg *PlayerConfig) {
		if n > 0 {
			cfg.QueueLength = n
		}
	}
}

// SendTimeout is number of milliseconds the player will wait to attempt to send an audio frame before moving onto the next song.
func SendTimeout(n int) PlayerOption {
	return func(cfg *PlayerConfig) {
		if n > 0 {
			cfg.SendTimeout = n
		}
	}
}

// IdleTimeout is the number of milliseconds the player will wait for another Payload before joining the idle channel.
func IdleTimeout(n int) PlayerOption {
	return func(cfg *PlayerConfig) {
		if n > 0 {
			cfg.IdleTimeout = n
		}
	}
}

// Player
type Player struct {
	discord *discordgo.Session
	cfg     *PlayerConfig
	// mutex to prevent concurrent invocations of enqueue from getting around QueueLength
	mu sync.Mutex
	// can't use a ringbuffer since it's bugged and uses all the cpu
	queue *queue.Queue
	ctrl  chan control
	quit  chan struct{}
}

// Connect launches a Player that dispatches voice to a discord guild.
// Since discord allows only one voice connection per guild, you should call Player.Quit before calling Connect again for the same guild.
func Connect(discord *discordgo.Session, guildID string, idleChannelID string, opts ...PlayerOption) *Player {
	cfg := DefaultConfig
	for _, opt := range opts {
		opt(&cfg)
	}
	// cfg no longer changes after applying options
	// don't need to use mutex to safely access its properties

	queue := queue.New(int64(cfg.QueueLength))
	quit := make(chan struct{})

	player := &Player{
		discord: discord,
		cfg:     &cfg,
		queue:   queue,
		quit:    quit,
	}

	if !validVoiceChannel(discord, idleChannelID) {
		idleChannelID = ""
	}

	go sender(player, discord, guildID, idleChannelID)

	return player
}

// Enqueue puts an item at the end of the queue.
func (play *Player) Enqueue(channelID string, title string, songOpener SongOpener, opts ...SongOption) error {
	if !validVoiceChannel(play.discord, channelID) {
		return ErrInvalidVoiceChannel
	}

	// lock ensures concurrent calls to Enqueue do not get around QueueLength
	play.mu.Lock()
	defer play.mu.Unlock()
	if play.queue.Len() >= int64(play.cfg.QueueLength) {
		return ErrFull
	}

	s := &song{
		channelID:  channelID,
		open:       songOpener,
		title:      title,
		onStart:    func() {},
		onEnd:      func(time.Duration, error) {},
		onProgress: func(time.Duration, []time.Time) {},
		onPause:    func(time.Duration) {},
		onResume:   func(time.Duration) {},
	}

	for _, opt := range opts {
		opt(s)
	}

	err := play.queue.Put(s)

	return err
}

// Length returns the number of items in the queue.
func (play *Player) Length() int {
	return int(play.queue.Len())
}

// Next returns the title of the next item in the queue, or
// the empty string if there is there is no item.
func (play *Player) Next() string {
	if item, err := play.queue.Peek(); err == nil {
		if s, ok := item.(*song); ok {
			return s.title
		}
	}
	return ""
}

// Clear removes all items from the queue.
// Clear does not skip the current song.
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

// Skip moves to the next item in the queue when an item is playing or is paused.
// Skip has no effect if no item is playing or is paused
// or if the player is already processing a skip or a pause for the current item.
// i.e. you cannot queue up future skips/pauses.
func (play *Player) Skip() error {
	// lock prevents concurrent write to play.ctrl in sendSong
	// TODO mutex in order to send on a channel is probably an antipattern and should be reconsidered
	play.mu.Lock()
	defer play.mu.Unlock()
	if play.ctrl == nil {
		return errors.New("nothing to skip")
	}
	select {
	case play.ctrl <- skip:
	default:
		return errors.New("busy")
	}
	return nil
}

// Pause stops the currently playing item or resumes the currently paused item.
// Pause has no effect if no item is playing or is paused
// or if the player is already processing a skip or a pause for the current item.
// i.e. you cannot queue up future skips/pauses.
func (play *Player) Pause() error {
	// lock prevents concurrent write to play.ctrl in sendSong
	// TODO mutex in order to send on a channel is probably an antipattern and should be reconsidered
	play.mu.Lock()
	defer play.mu.Unlock()
	if play.ctrl == nil {
		return errors.New("nothing to pause")
	}
	select {
	case play.ctrl <- pause:
	default:
		return errors.New("busy")
	}
	return nil
}

// Quit closes the player.
// You should call quit before calling connect again for the same guild.
func (play *Player) Quit() {
	// lock prevents concurrent call to quit from closing play.quit twice
	play.mu.Lock()
	defer play.mu.Unlock()
	if play.queue.Disposed() {
		return
	}

	items := play.queue.Dispose()
	for _, item := range items {
		if s, ok := item.(*song); ok {
			s.onEnd(0, errors.New("quit"))
		}
	}
	close(play.quit)
}

func validVoiceChannel(discord *discordgo.Session, channelID string) bool {
	channel, err := discord.State.Channel(channelID)
	if err != nil {
		channel, err = discord.Channel(channelID)
	}
	return err == nil && channel.Type == discordgo.ChannelTypeGuildVoice
}
