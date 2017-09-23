package discordvoice

import (
	"errors"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/jonas747/dca"
)

const version = "0.0.1"

// VoiceConfig
type VoiceConfig struct {
	QueueLength        int `toml:"queue_length"`
	SendTimeout        int `toml:"send_timeout"`
	IdleTimeout        int `toml:"afk_timeout"`
	CanBroadcastStatus bool
}

var DefaultConfig = VoiceConfig{
	QueueLength: 100,
	SendTimeout: 1000,
	IdleTimeout: 300,
}

type VoiceOption func(*VoiceConfig)

func QueueLength(n int) VoiceOption {
	return func(cfg *VoiceConfig) {
		if n > 0 {
			cfg.QueueLength = n
		}
	}
}

func SendTimeout(n int) VoiceOption {
	return func(cfg *VoiceConfig) {
		if n > 0 {
			cfg.SendTimeout = n
		}
	}
}

func IdleTimeout(n int) VoiceOption {
	return func(cfg *VoiceConfig) {
		if n > 0 {
			cfg.IdleTimeout = n
		}
	}
}

func CanBroadcastStatus(b bool) VoiceOption {
	return func(cfg *VoiceConfig) {
		cfg.CanBroadcastStatus = b
	}
}

type control uint8

const (
	nop control = iota
	skip
	pause
)

// Payload
type Payload struct {
	PreEncoded bool
	Reader     io.Reader
	URL        string
	ChannelID  string
	Volume     int
	Name       string
	Duration   time.Duration
}

var ErrSendFull = errors.New("Full send buffer")

type Player struct {
	quit    chan struct{}
	queue   chan<- *Payload
	control chan<- control
}

// Enqueue puts an item at the end of the queue
func (play *Player) Enqueue(p *Payload) error {
	select {
	case play.queue <- p:
	default:
		return ErrSendFull
	}
	return nil
}

// Length returns the number of items in the queue
func (play *Player) Length() int {
	return len(play.queue)
}

// Skip moves to the next item in the queue when an item is playing or is paused
// Skip does nothing when there is no item in the queue or the player is already processing a control
func (play *Player) Skip() error {
	select {
	case play.control <- skip:
	default:
		return ErrSendFull
	}
	return nil
}

// Pause stops the currently playing item or resumes the currently paused item
// Pause does nothing when there is no item in the queue or the player is already processing a control
func (play *Player) Pause() error {
	select {
	case play.control <- pause:
	default:
		return ErrSendFull
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

type receives struct {
	quit  <-chan struct{}
	queue <-chan *Payload
	ctrl  <-chan control
}

// Connect launches a Player that dispatches voice to a discord guild
// Since discord allows only one voice connection per guild, you should call Player.Quit before calling connect again for the same guild
func Connect(s *discordgo.Session, guildID string, idleChannelID string, opts ...VoiceOption) *Player {
	cfg := DefaultConfig
	for _, opt := range opts {
		opt(&cfg)
	}

	quit := make(chan struct{})
	queue := make(chan *Payload, cfg.QueueLength)
	ctrl := make(chan control, 1)

	// coerce channels to receieve/send-only in structs
	recv := &receives{
		quit:  quit,
		queue: queue,
		ctrl:  ctrl,
	}

	send := &Player{
		quit:    quit,
		queue:   queue,
		control: ctrl,
	}

	go payloadSender(recv, s, guildID, idleChannelID, cfg.CanBroadcastStatus, cfg.SendTimeout, cfg.IdleTimeout)

	return send
}

func payloadSender(recv *receives, s *discordgo.Session, guildID string, idleChannelID string, canSetStatus bool, sendTimeout int, idleTimeout int) {
	if recv.quit == nil || recv.queue == nil {
		return
	}

	var vc *discordgo.VoiceConnection
	var err error

	var p *Payload
	var ok bool

	var idleTimer <-chan time.Time

	disconnect := func() {
		if vc != nil {
			_ = vc.Disconnect()
			vc = nil
		}
	}

	join := func(channelID string) (*discordgo.VoiceConnection, error) {
		if vc == nil || vc.ChannelID != p.ChannelID {
			return s.ChannelVoiceJoin(guildID, channelID, false, true)
		}
		return vc, nil
	}

	setStatus := func(status string) {}
	if canSetStatus {
		setStatus = func(status string) {
			if len(status) > 32 {
				status = status[:32]
			}
			err := s.GuildMemberNickname(guildID, "@me", status)
			if err != nil {
				log.Printf("error set nickname %v", err)
			}
		}
	}

	defer disconnect()

	vc, err = join(idleChannelID)
	if err != nil {
		log.Printf("error join idle channel %v", err)
	}

	for {
		// check quit signal between every payload without blocking
		// otherwise it would be possible for a quit signal to go ignored at least once if there is a continuous stream of voice payloads ready in queue
		// since when multiple cases in a select are ready at same time a case is selected randomly
		select {
		case <-recv.quit:
			return
		default:
		}

		select {
		case <-recv.quit:
			return
		// idletimer is started only once after each payload
		// not every time we enter this select, to prevent repeatedly rejoining idle channel
		case <-idleTimer:
			log.Printf("idle timeout in guild %v", vc.GuildID)
			vc, err = join(idleChannelID)
			if err != nil {
				log.Printf("error join idle channel %v", err)
			}
			continue
		case p, ok = <-recv.queue:
			if !ok {
				return
			}
		}

		vc, err = join(p.ChannelID)
		
		if err != nil {
			log.Printf("error join payload channel %v", err)
		} else {
			sendPayload(recv, p, setStatus, vc, sendTimeout)
		}
		idleTimer = time.NewTimer(time.Duration(idleTimeout) * time.Millisecond).C
	}
}

var defaultEncodeOptions = dca.EncodeOptions{
	Volume:           256,
	Channels:         2,
	FrameRate:        48000,
	FrameDuration:    20,
	Bitrate:          128,
	RawOutput:        false,
	Application:      dca.AudioApplicationAudio,
	CompressionLevel: 10,
	PacketLoss:       1,
	BufferedFrames:   100,
	VBR:              false,
	AudioFilter:      "",
}

func sendPayload(recv *receives, p *Payload, setStatus func(string), vc *discordgo.VoiceConnection, sendTimeout int) {
	var err error
	var paused bool
	var frame []byte

	log.Printf("begin send %v", p.Name)

	var elapsed time.Duration
	// use an anonymous function so it reads the value of elapsed in its closure instead of in its paramters
	// this way it prints the value of elapsed at the time it is executed rather than the time it is deferred
	defer func() { log.Printf("read %v of %v, expected %v", elapsed, p.Name, p.Duration) }()

	var opusReader dca.OpusReader
	if p.PreEncoded {
		opusReader = dca.NewDecoder(p.Reader)
	} else {
		resp, err := http.Get(p.URL)
		if err != nil {
			log.Printf("error request url %v", err)
		}
		log.Printf("resp %#v", resp)
		defer resp.Body.Close()
		opts := defaultEncodeOptions
		if 0 < p.Volume && p.Volume < 256 {
			opts.Volume = p.Volume
		}
		encoder, err := dca.EncodeMem(resp.Body, &opts)
		if err != nil {
			log.Printf("error encoding audio %v", err)
			return
		}
		defer encoder.Cleanup()
		opusReader = encoder
	}

	frameSize := opusReader.FrameDuration()
	log.Printf("frame size %v", frameSize)

	drain(recv.ctrl)
	vc.Speaking(true)
	defer vc.Speaking(false)

	if p.Name != "" {
		setStatus("🔊 " + p.Name)
		defer setStatus("")
	}

	for {
		select {
		case <-recv.quit:
			return
		case c, _ := <-recv.ctrl:
			switch c {
			case skip:
				return
			case pause:
				paused = !paused
			}
		default:
		}

		// TODO figure out a way to impl pause without repeating select block
		if paused {
			setStatus("⏸️ " + p.Name)
			select {
			case <-recv.quit:
				return
			case c, _ := <-recv.ctrl:
				switch c {
				case skip:
					return
				case pause:
					paused = !paused
					setStatus("🔊 " + p.Name)

				}
			}
		}

		// underlying impl is encoding/binary.Read
		// err is EOF iff no bytes were read
		// err is UnexpectedEOF if partial frame is read
		frame, err = opusReader.OpusFrame()
		if err != nil {
			log.Printf("error reading frame %v", err)
			return
		}

		select {
		case <-recv.quit:
			return
		case vc.OpusSend <- frame:
		case <-time.After(time.Duration(sendTimeout) * time.Millisecond):
			log.Printf("send timeout on %#v", vc)
			return
		}

		elapsed += frameSize
	}
}

func drain(c <-chan control) {
	for {
		select {
		case <-c:
		default:
			return
		}
	}
}
