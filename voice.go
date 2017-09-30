package discordvoice

import (
	"io"
	"log"
	"net/http"
	"time"

	"github.com/Workiva/go-datastructures/queue"
	"github.com/bwmarrin/discordgo"
	"github.com/jonas747/dca"
)

const Version = "0.0.2"

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

func payloadSender(play *Player, s *discordgo.Session, guildID string, idleChannelID string, canSetStatus bool, sendTimeout int, idleTimeout int) {
	if play.quit == nil || play.queue == nil || play.queue.Disposed() {
		return
	}

	var vc *discordgo.VoiceConnection
	var err error

	var items []interface{}
	var p *Payload
	var ok bool

	// var isIdle bool
	pollTimeout := time.Duration(idleTimeout) * time.Millisecond

	disconnect := func() {
		if vc != nil {
			_ = vc.Disconnect()
			vc = nil
		}
	}

	join := func(channelID string) (*discordgo.VoiceConnection, error) {
		if vc == nil || vc.ChannelID != channelID {
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

		items, err = play.queue.Poll(1, pollTimeout)

		if err == queue.ErrTimeout {
			pollTimeout = 0
			vc, err = join(idleChannelID)
			if err != nil {
				log.Printf("error join idle channel %v", err)
			}
			continue
		} else if err != nil {
			// disposed
			return
		} else if len(items) != 1 {
			// should not be possible, but avoid a panic just in case
			continue
		}

		p, ok = items[0].(*Payload)
		if !ok {
			// should not be possible, but avoid a panic just in case
			pollTimeout = time.Duration(idleTimeout) * time.Millisecond
			continue
		}

		vc, err = join(p.ChannelID)
		if err != nil {
			log.Printf("error join payload channel %v", err)
		} else {
			sendPayload(play, p, setStatus, vc, sendTimeout)
		}
		pollTimeout = time.Duration(idleTimeout) * time.Millisecond
	}
}

func sendPayload(play *Player, p *Payload, setStatus func(string), vc *discordgo.VoiceConnection, sendTimeout int) {
	var err error
	var paused bool
	var frame []byte

	log.Printf("begin send %v", p.Name)

	var elapsed time.Duration
	// use an anonymous function so it reads the value of elapsed in its closure instead of in its paramters
	// this way log.Printf prints the value of elapsed at the time it is executed rather than the time it is deferred
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

	drain(play.control)
	vc.Speaking(true)
	defer vc.Speaking(false)

	if p.Name != "" {
		setStatus("🔊 " + p.Name)
		defer setStatus("")
	}

	for {
		select {
		case <-play.quit:
			return
		case c, _ := <-play.control:
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
			case <-play.quit:
				return
			case c, _ := <-play.control:
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
		case <-play.quit:
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
