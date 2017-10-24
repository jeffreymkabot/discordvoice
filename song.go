package discordvoice

import (
	"io"
	"time"
)

// SongOption
type SongOption func(*song)

// PreEncoded
func PreEncoded(r io.Reader) SongOption {
	return func(s *song) {
		s.preencoded = true
		s.reader = r
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

// Title tells the player the song is named something other than its url
func Title(t string) SongOption {
	return func(s *song) {
		s.title = t
	}
}

// Duration lets the player know how long it should expect the song to be
func Duration(d time.Duration) SongOption {
	return func(s *song) {
		s.duration = d
	}
}

func OnStart(f func()) SongOption {
	return func(s *song) {
		s.onStart = f
	}
}

func OnEnd(f func()) SongOption {
	return func(s *song) {
		s.onEnd = f
	}
}

// OnProgress interval is approximate, will be quantized to frame duration
func OnProgress(f func(elapsed time.Duration), interval time.Duration) SongOption {
	return func(s *song) {
		s.onProgress = f
		s.progressInterval = interval
	}
}

func OnPause(f func(elapsed time.Duration)) SongOption {
	return func(s *song) {
		s.onPause = f
	}
}

func OnResume(f func(elapsed time.Duration)) SongOption {
	return func(s *song) {
		s.onResume = f
	}
}

type song struct {
	channelID string

	url string

	preencoded bool
	reader     io.Reader

	filters  string
	loudness float64

	title    string
	duration time.Duration

	onStart          func()
	onEnd            func()
	onProgress       func(elapsed time.Duration)
	progressInterval time.Duration
	onPause          func(elapsed time.Duration)
	onResume         func(elapsed time.Duration)
}