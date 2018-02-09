package discordvoice

import (
	"io"
	"time"
)

// SongOption
type SongOption func(*song)

// PreEncoded
func PreEncoded() SongOption {
	return func(s *song) {
		s.preencoded = true
	}
}

// Filter ffmpeg audio filter string
func Filter(af string) SongOption {
	return func(s *song) {
		s.filters = af
	}
}

// Loudness sets the loudness target.  Higher is louder.
// See https://ffmpeg.org/ffmpeg-filters.html#loudnorm.
// Values less than -70.0 or greater than -5.0 have no effect.
// In particular, the default value of 0 has no effect and input streams will be unchanged.
func Loudness(f float64) SongOption {
	return func(s *song) {
		s.loudness = f
	}
}

// Duration lets the player know how long it should expect the song to be.
func Duration(d time.Duration) SongOption {
	return func(s *song) {
		s.duration = d
	}
}

func OnStart(f func()) SongOption {
	return func(s *song) {
		if f != nil {
			s.onStart = f
		}
	}
}

// OnEnd
func OnEnd(f func(elapsed time.Duration, err error)) SongOption {
	return func(s *song) {
		if f != nil {
			s.onEnd = f
		}
	}
}

// OnProgress interval is approximate, will be quantized to a multiple of frame duration.
func OnProgress(f func(elapsed time.Duration, frameTime []time.Time), interval time.Duration) SongOption {
	return func(s *song) {
		if f != nil {
			s.onProgress = f
			s.progressInterval = interval
		}
	}
}

func OnPause(f func(elapsed time.Duration)) SongOption {
	return func(s *song) {
		if f != nil {
			s.onPause = f
		}
	}
}

func OnResume(f func(elapsed time.Duration)) SongOption {
	return func(s *song) {
		if f != nil {
			s.onResume = f
		}
	}
}

type SongOpener func() (io.ReadCloser, error)

type song struct {
	channelID string

	open SongOpener

	preencoded bool

	filters  string
	loudness float64

	title    string
	duration time.Duration

	onStart          func()
	onEnd            func(elapsed time.Duration, err error)
	onProgress       func(elapsed time.Duration, frameTimes []time.Time)
	progressInterval time.Duration
	onPause          func(elapsed time.Duration)
	onResume         func(elapsed time.Duration)
}
