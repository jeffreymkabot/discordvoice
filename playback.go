package player

import (
	"fmt"
	"io"
	"time"

	"github.com/jonas747/dca"
	"github.com/pkg/errors"
)

func playback(player *Player, device Device) {
	player.wg.Add(1)
	// isIdle := pollTimeout == 0
	pollTimeout := time.Duration(player.cfg.IdleTimeout) * time.Millisecond

	for {
		song, err := player.poll(pollTimeout)
		if err == errPollTimeout {
			pollTimeout = 0
			player.cfg.Idle()
			continue
		} else if err != nil {
			if player.writer != nil {
				player.writer.Close()
			}
			player.wg.Done()
			return
		}
		pollTimeout = time.Duration(player.cfg.IdleTimeout) * time.Millisecond

		player.wg.Add(1)
		elapsed, err := openAndPlay(player, song, device)
		song.onEnd(elapsed, err)
		player.wg.Done()
	}
}

func openAndPlay(player *Player, song *songItem, device Device) (elapsed time.Duration, err error) {
	var reader dca.OpusReader
	var writer io.WriteCloser

	// TODO consider how to manage closing abstract writers and not just discord voice channels
	writer, err = device.Open(song.channelID)
	if err != nil {
		err = errors.Wrap(err, "failed to join song channel")
		return
	}
	// keep track of the open writer

	player.writer = writer

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
	var frame []byte
	nWrites, frameDur := 0, src.FrameDuration()

	var writeInterval int
	var writeLatencies []time.Duration
	var prevWriteTime time.Time
	if cb.progressInterval > 0 {
		writeInterval = int(cb.progressInterval / frameDur)
		writeLatencies = make([]time.Duration, 0, writeInterval)
	}

	// drain any buffered control signals (e.g. client called Skip() before any song was queued)
	drain(player.ctrl)

	// gate reads and writes in order to respect and pause/skip signals
	ticker := time.NewTicker(frameDur / 2)
	defer ticker.Stop()
	// playing if ready == ticker, paused if ready == nil
	ready := ticker.C

	cb.onStart()
	for {
		select {
		case <-player.quit:
			err = ErrClosed
			return
		case c := <-player.ctrl:
			switch c {
			case skip:
				err = ErrSkipped
				return
			case pause:
				if ready != nil {
					cb.onPause(elapsed)
					ready = nil
				} else {
					cb.onResume(elapsed)
					ready = ticker.C
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
				now := time.Now()
				if !prevWriteTime.IsZero() {
					writeLatencies = append(writeLatencies, now.Sub(prevWriteTime))
				}
				prevWriteTime = now
				if nWrites%writeInterval == 0 {
					tmp := make([]time.Duration, len(writeLatencies))
					copy(tmp, writeLatencies)
					writeLatencies = writeLatencies[len(writeLatencies):]
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
