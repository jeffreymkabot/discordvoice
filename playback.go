package player

import (
	"fmt"
	"io"
	"time"

	"github.com/jonas747/dca"
	"github.com/pkg/errors"
)

func playback(player *Player, opener WriterOpener, idle func()) {
	// isIdle := pollTimeout == 0
	pollTimeout := time.Duration(player.cfg.IdleTimeout) * time.Millisecond

	for {
		song, err := player.poll(pollTimeout)
		if err == errPollTimeout {
			pollTimeout = 0
			idle()
			continue
		} else if err != nil {
			return
		}
		pollTimeout = time.Duration(player.cfg.IdleTimeout) * time.Millisecond

		player.wg.Add(1)
		elapsed, err := openAndPlay(player, song, opener)
		song.onEnd(elapsed, err)
		player.wg.Done()
	}
}

func openAndPlay(player *Player, song *songItem, opener WriterOpener) (elapsed time.Duration, err error) {
	var reader dca.OpusReader
	var writer io.WriteCloser

	writer, err = opener.Open(song.channelID)
	if err != nil {
		err = errors.Wrap(err, "failed to join song channel")
		return
	}

	rc, err := song.open()
	if err != nil {
		err = errors.Wrap(err, "failed to open song")
		return
	}
	defer rc.Close()

	if song.preencoded {
		reader = dca.NewDecoder(rc)
	} else {
		opts := defaultEncodeOptions
		opts.AudioFilter = song.filters
		if song.loudness != 0 {
			if opts.AudioFilter != "" {
				opts.AudioFilter += ", "
			}
			opts.AudioFilter += fmt.Sprintf("loudnorm=i=%.1f", song.loudness)
		}

		var enc *dca.EncodeSession
		enc, err = dca.EncodeMem(rc, &opts)
		if err != nil {
			err = errors.Wrap(err, "failed to start encoder")
			return
		}
		defer enc.Cleanup()
		reader = enc
	}

	elapsed, err = play(player, reader, writer, song.callbacks)
	return
}

func play(player *Player, src dca.OpusReader, dst io.Writer, cb callbacks) (elapsed time.Duration, err error) {
	sig := make(chan struct{})
	close(sig)
	// playing if ready == sig, paused if ready == nil
	ready := sig

	var frame []byte
	nWrites, frameDur := 0, src.FrameDuration()

	var writeInterval int
	var writeTimes []time.Time
	if cb.progressInterval > 0 {
		writeInterval = int(cb.progressInterval / frameDur)
		writeTimes = make([]time.Time, writeInterval, writeInterval)
	}

	// drain any buffered control signals (e.g. client called Skip() before any song was queued)
	drain(player.ctrl)

	cb.onStart()
	for {
		select {
		case <-player.quit:
			err = errors.New("quit")
			return
		case c := <-player.ctrl:
			switch c {
			case skip:
				err = errors.New("skipped")
				return
			case pause:
				if ready != nil {
					cb.onPause(elapsed)
					ready = nil
				} else {
					cb.onResume(elapsed)
					ready = sig
				}
			}
		case <-ready:
			frame, err = src.OpusFrame()
			if err != nil {
				err = errors.Wrap(err, "failed to read frame")
				// include some extra debug info if failed well before we should have
				if cb.duration > 0 && cb.duration-elapsed > 1*time.Second {
					if enc, ok := src.(*dca.EncodeSession); ok {
						err = errors.WithMessage(err, enc.FFMPEGMessages())
					}
				}
				return
			}
			_, err = dst.Write(frame)
			if err != nil {
				err = errors.Wrap(err, "failed to write frame")
				return
			}

			nWrites++
			elapsed = time.Duration(nWrites) * frameDur

			// only invoke onProgress callback if given a valid progressInterval
			if writeInterval > 0 {
				writeTimes[(nWrites-1)%writeInterval] = time.Now()
				if nWrites > 0 && nWrites%writeInterval == 0 {
					var tmp []time.Time
					copy(tmp, writeTimes)
					cb.onProgress(elapsed, tmp)
				}
			}
		}
	}
}

func drain(ctrl <-chan control) {
	for {
		select {
		case <-ctrl:
		default:
			return
		}
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
