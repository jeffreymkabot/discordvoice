package discordvoice

import (
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/Workiva/go-datastructures/queue"
	"github.com/bwmarrin/discordgo"
	"github.com/jonas747/dca"
)

const Version = "0.1.0"

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

func sender(player *Player, session *discordgo.Session, guildID string, idleChannelID string) {
	if player.queue == nil || player.queue.Disposed() {
		return
	}

	var vc *discordgo.VoiceConnection
	var err error

	var items []interface{}
	var s *song
	var ok bool

	// isIdle := pollTimeout == 0
	pollTimeout := time.Duration(player.cfg.IdleTimeout) * time.Millisecond

	// join interacts with vc in closure
	// join("") to disconnect
	join := func(channelID string) error {
		if channelID == "" && vc != nil {
			return vc.Disconnect()
		}

		// read vc.ChannelID into a temp variable because ChannelVoiceJoin takes its own write mutex on vc
		vcChannelID := ""
		if vc != nil {
			vc.RLock()
			vcChannelID = vc.ChannelID
			vc.RUnlock()
		}

		// don't attempt to join a channel we are already in
		if vc == nil || vcChannelID != channelID {
			vc, err = session.ChannelVoiceJoin(guildID, channelID, false, true)
			return err
		}

		return nil
	}

	defer join("")

	err = join(idleChannelID)
	if err != nil {
		log.Printf("error join idle channel %v", err)
	}

	for {
		items, err = player.queue.Poll(1, pollTimeout)
		if err == queue.ErrTimeout {
			pollTimeout = 0
			err = join(idleChannelID)
			if err != nil {
				log.Printf("error join idle channel %v", err)
			}
			continue
		} else if err != nil {
			// disposed, play.Quit() was called
			return
		} else if len(items) != 1 {
			// should not be possible, but avoid a panic just in case
			pollTimeout = time.Duration(player.cfg.IdleTimeout) * time.Millisecond
			continue
		}

		s, ok = items[0].(*song)
		if !ok {
			// should not be possible, but avoid a panic just in case
			pollTimeout = time.Duration(player.cfg.IdleTimeout) * time.Millisecond
			continue
		}

		err = join(s.channelID)
		if err != nil {
			log.Printf("error join payload channel %v", err)
		} else {
			sendSong(player, s, vc)
		}
		s.onEnd()
		pollTimeout = time.Duration(player.cfg.IdleTimeout) * time.Millisecond
	}
}

func sendSong(play *Player, s *song, vc *discordgo.VoiceConnection) {
	var paused bool
	var frame []byte

	var frameCount int
	var frameSize time.Duration

	var frameInterval int

	log.Printf("begin send %v", s.title)

	opusReader, cleanup, err := opusReader(s)
	if err != nil {
		log.Printf("error streaming file %v", err)
		return
	}
	defer cleanup()
	frameSize = opusReader.FrameDuration()
	if s.progressInterval > 0 {
		frameInterval = int(s.progressInterval / frameSize)
	}

	sendTimeout := time.Duration(time.Duration(play.cfg.SendTimeout) * time.Millisecond)

	// use an anonymous function so it reads the value of elapsed in its closure instead of in its paramters
	// this way log.Printf prints the value of elapsed at the time it is executed rather than the time it is deferred
	defer func() {
		log.Printf("read %v of %v, expected %v", time.Duration(frameCount)*frameSize, s.title, s.duration)
	}()

	drain(play.control)

	vc.Speaking(true)
	defer vc.Speaking(false)

	s.onStart()

	for {
		select {
		case c, ok := <-play.control:
			if !ok {
				// play.Quit was called
				return
			}
			switch c {
			case skip:
				return
			case pause:
				paused = !paused
			}
		default:
		}

		// pause impl is same select as above with no default and status updates on pause/resume
		// TODO is it possible to impl pause without repeating select block
		if paused {
			s.onPause(time.Duration(frameCount) * frameSize)

			select {
			case c, ok := <-play.control:
				if !ok {
					// play.Quit was called
					return
				}
				switch c {
				case skip:
					return
				case pause:
					paused = !paused
					s.onResume(time.Duration(frameCount) * frameSize)
				}
			}
		}

		// underlying impl is encoding/binary.Read
		// err is EOF iff no bytes were read
		// err is UnexpectedEOF if partial frame is read
		frame, err = opusReader.OpusFrame()
		if err != nil {
			log.Printf("error reading frame %v", err)
			// see if this was significantly before the expected end of the song
			deviation := s.duration - time.Duration(frameCount)*frameSize
			if deviation < 0 {
				deviation *= -1
			}
			if s.duration > 0 && deviation > time.Second {
				log.Print("early EOF :(")
				if enc, ok := opusReader.(*dca.EncodeSession); ok {
					log.Printf("ffmpegout %#v", enc.FFMPEGMessages())
				}
			}
			return
		}

		select {
		case vc.OpusSend <- frame:
		case <-time.After(sendTimeout):
			log.Printf("send timeout on %#v", vc)
			return
		}

		// send a status every n frames
		if frameInterval > 0 && frameCount%frameInterval == 0 {
			s.onProgress(time.Duration(frameCount) * frameSize)
		}

		frameCount++
	}
}

// wrap an opusreader around the song source
func opusReader(s *song) (dca.OpusReader, func(), error) {
	if s.preencoded {
		return dca.NewDecoder(s.reader), func() {}, nil
	}
	resp, err := http.Get(s.url)
	if err != nil {
		return nil, func() {}, err
	}
	log.Printf("resp %#v", resp)
	opts := defaultEncodeOptions
	opts.AudioFilter = s.filters
	if s.loudness != 0 {
		if opts.AudioFilter != "" {
			opts.AudioFilter += ", "
		}
		opts.AudioFilter += fmt.Sprintf("loudnorm=i=%.1f", s.loudness)
	}
	encoder, err := dca.EncodeMem(resp.Body, &opts)
	if err != nil {
		resp.Body.Close()
		return nil, func() {}, err
	}
	return encoder, func() { resp.Body.Close(); encoder.Cleanup() }, err
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
