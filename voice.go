package discordvoice

import (
	"fmt"
	"log"
	"time"

	"github.com/Workiva/go-datastructures/queue"
	"github.com/bwmarrin/discordgo"
	"github.com/jonas747/dca"
	"github.com/pkg/errors"
)

const Version = "0.3.0"

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
		items, err := player.queue.Poll(1, pollTimeout)
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

		s, ok := items[0].(*song)
		if !ok {
			// should not be possible, but avoid a panic just in case
			pollTimeout = time.Duration(player.cfg.IdleTimeout) * time.Millisecond
			continue
		}

		err = join(s.channelID)
		if err != nil {
			s.onEnd(0, errors.Wrap(err, "failed to join song channel"))
		} else {
			s.onEnd(sendSong(player, s, vc))
		}
		pollTimeout = time.Duration(player.cfg.IdleTimeout) * time.Millisecond
	}
}

func sendSong(play *Player, s *song, vc *discordgo.VoiceConnection) (time.Duration, error) {
	opusReader, cleanup, err := opusReader(s)
	if err != nil {
		return 0, errors.Wrap(err, "failed to stream url / file")
	}
	defer cleanup()

	frameCount := 0
	frameSize := opusReader.FrameDuration()
	frameInterval := 0
	var frameTimes []time.Time
	if s.progressInterval > 0 {
		frameInterval = int(s.progressInterval / frameSize)
		frameTimes = make([]time.Time, frameInterval, frameInterval)
	}

	sendTimeout := time.Duration(time.Duration(play.cfg.SendTimeout) * time.Millisecond)

	vc.Speaking(true)
	defer vc.Speaking(false)

	// lock prevents concurrent read of play.ctrl in Pause() / Skip()
	play.mu.Lock()
	play.ctrl = make(chan control, 1)
	play.mu.Unlock()
	defer func() {
		play.mu.Lock()
		play.ctrl = nil
		play.mu.Unlock()
	}()

	paused := false
	s.onStart()

	for {
		select {
		case <-play.quit:
			return time.Duration(frameCount) * frameSize, errors.New("quit")
		case c := <-play.ctrl:
			switch c {
			case skip:
				return time.Duration(frameCount) * frameSize, errors.New("skipped")
			case pause:
				paused = !paused
			}
		default:
		}

		// TODO is it possible to impl pause without repeating code
		if paused {
			s.onPause(time.Duration(frameCount) * frameSize)

			select {
			case <-play.quit:
				return time.Duration(frameCount) * frameSize, errors.New("quit")
			case c := <-play.ctrl:
				switch c {
				case skip:
					return time.Duration(frameCount) * frameSize, errors.New("skipped")
				case pause:
					paused = !paused
					s.onResume(time.Duration(frameCount) * frameSize)
				}
			}
		}

		// underlying impl is encoding/binary.Read
		// err is EOF iff no bytes were read
		// err is UnexpectedEOF if partial frame is read
		frame, err := opusReader.OpusFrame()
		if err != nil {
			// see if this was significantly before the expected end of the song
			deviation := s.duration - time.Duration(frameCount)*frameSize
			if deviation < 0 {
				deviation *= -1
			}
			if s.duration > 0 && deviation > 300*time.Millisecond {
				if enc, ok := opusReader.(*dca.EncodeSession); ok {
					err = errors.WithMessage(err, enc.FFMPEGMessages())
				}
			}
			return time.Duration(frameCount) * frameSize, errors.Wrap(err, "failed to read frame")
		}

		// send a status every n frames
		if frameInterval > 0 && frameCount > 0 && frameCount%frameInterval == 0 {
			// careful frameTimes is pointer to array and if onProgress is async frameTimes will change while onProgress uses it
			// TODO why are the first three frameTimes in every frameInterval sometimes equal
			// vc.OpusSend is buffered to length=2 but its read-interval is 20ms
			// so I don't think copying here could delay three reads that the buffer clears
			cpy := make([]time.Time, len(frameTimes))
			copy(cpy, frameTimes)
			s.onProgress(time.Duration(frameCount)*frameSize, cpy)
		}

		select {
		case vc.OpusSend <- frame:
			if frameInterval > 0 {
				frameTimes[frameCount%frameInterval] = time.Now()
			}
		case <-time.After(sendTimeout):
			return time.Duration(frameCount) * frameSize, errors.Errorf("send timeout on voice connection %#v", vc)
		}

		frameCount++
	}
}

// wrap an opusreader around the song source
func opusReader(s *song) (dca.OpusReader, func(), error) {
	readCloser, err := s.open()
	if err != nil {
		return nil, func() {}, err
	}

	if s.preencoded {
		return dca.NewDecoder(readCloser), func() { readCloser.Close() }, nil
	}

	opts := defaultEncodeOptions
	opts.AudioFilter = s.filters
	if s.loudness != 0 {
		if opts.AudioFilter != "" {
			opts.AudioFilter += ", "
		}
		opts.AudioFilter += fmt.Sprintf("loudnorm=i=%.1f", s.loudness)
	}

	encoder, err := dca.EncodeMem(readCloser, &opts)
	if err != nil {
		readCloser.Close()
		return nil, func() {}, err
	}
	return encoder, func() { readCloser.Close(); encoder.Cleanup() }, err
}
