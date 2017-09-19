package discordvoice

import (
	"io"
	"log"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/jonas747/dca"
)

// VoiceConfig
type VoiceConfig struct {
	QueueLength int `toml:"queue_length"`
	SendTimeout int `toml:"send_timeout"`
	IdleTimeout int `toml:"afk_timeout"`
	Broadcast bool
}

var DefaultConfig = VoiceConfig{
	QueueLength: 100,
	SendTimeout: 1000,
	IdleTimeout: 300,
}

type Control uint8

const (
	nop Control = iota
	Skip
	Pause
)

// Payload
type Payload struct {
	DCAEncoded bool
	ChannelID  string
	Reader     io.Reader
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

type Sends struct {
	Queue   chan<- *Payload
	Control chan<- Control
}

// Connect launches a goroutine that dispatches voice to a discord guild
// Sends
// Close
// Since discord allows only one voice connection per guild, you should call close before calling connect again for the same guild
func Connect(s *discordgo.Session, guildID string, idleChannelID string, opts ...VoiceOption) (Sends, func()) {
	cfg := DefaultConfig
	for _, opt := range opts {
		opt(&cfg)
	}

	// close quit channel and all attempts to receive it will receive it without blocking
	// quit channel is hidden from the outside world
	// accessed only through closure for close func
	quit := make(chan struct{})
	close := func() {
		select {
		case <-quit:
			// already closed, don't close a closed channel
			return
		default:
			close(quit)
		}
	}

	queue := make(chan *Payload, cfg.QueueLength)
	ctrl := make(chan Control, 1)

	join := func(channelID string) (*discordgo.VoiceConnection, error) {
		return s.ChannelVoiceJoin(guildID, channelID, false, true)
	}

	// coerce queue and quit to receieve/send-only in structs
	recv := receives{
		quit:  quit,
		queue: queue,
		ctrl:  ctrl,
	}
	send := Sends{
		Queue:   queue,
		Control: ctrl,
	}

	go payloadSender(recv, join, idleChannelID, cfg.SendTimeout, cfg.IdleTimeout)
	// coerce queue to send-only in voicebox
	return send, close
}

type receives struct {
	quit  <-chan struct{}
	queue <-chan *Payload
	ctrl  <-chan Control
}

func payloadSender(recv receives, join func(cID string) (*discordgo.VoiceConnection, error), idleChannelID string, sendTimeout int, idleTimeout int) {
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

	defer disconnect()

	vc, _ = join(idleChannelID)

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
				disconnect()
			}
			continue
		case p, ok = <-recv.queue:
			if !ok {
				return
			}
		}

		vc, err = join(p.ChannelID)
		if err != nil {
			log.Printf("Error join payload channel %v", err)
		} else {
			sendPayload(recv, p, vc, sendTimeout)
		}
		if closer, ok := p.Reader.(io.Closer); ok {
			closer.Close()
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
	VBR:              true,
}

func sendPayload(recv receives, p *Payload, vc *discordgo.VoiceConnection, sendTimeout int) {
	var err error
	var paused bool
	var frame []byte
	reader := p.Reader
	if !p.DCAEncoded {
		encoder, err := dca.EncodeMem(p.Reader, &defaultEncodeOptions)
		if err != nil {
			log.Printf("error encoding audio %v", err)
			return
		}
		defer encoder.Cleanup()
		reader = encoder
	}
	opusReader := dca.NewDecoder(reader)
	err = opusReader.ReadMetadata()
	if err != nil {
		log.Printf("metadata %#v", opusReader.Metadata)
	} else {
		log.Printf("no metadata :(")
	}
	vc.Speaking(true)
	defer vc.Speaking(false)
	for {
		select {
		case <-recv.quit:
			return
		case c, _ := <-recv.ctrl:
			switch c {
			case Skip:
				return
			case Pause:
				paused = !paused
			}
		default:
		}

		if paused {
			select {
			case <-recv.quit:
				return
			case c, _ := <-recv.ctrl:
				switch c {
				case Skip:
					return
				case Pause:
					paused = !paused
				}
			}
		}

		// underlying impl is encoding/binary.Read
		// err is EOF iff no bytes were read
		// err is UnexpectedEOF if partial frame is read
		frame, err = opusReader.OpusFrame()
		if err != nil {
			return
		}

		select {
		case <-recv.quit:
		case vc.OpusSend <- frame:
		case <-time.After(time.Duration(sendTimeout) * time.Millisecond):
			return
		}
	}
}
