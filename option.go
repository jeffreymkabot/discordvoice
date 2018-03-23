package player

import "time"

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

// SongOption
type SongOption func(*songItem)

// PreEncoded
func PreEncoded() SongOption {
	return func(s *songItem) {
		s.preencoded = true
	}
}

// Filter ffmpeg audio filter string
func Filter(af string) SongOption {
	return func(s *songItem) {
		s.filters = af
	}
}

// Loudness sets the loudness target.  Higher is louder.
// See https://ffmpeg.org/ffmpeg-filters.html#loudnorm.
// Values less than -70.0 or greater than -5.0 have no effect.
// In particular, the default value of 0 has no effect and input streams will be unchanged.
func Loudness(f float64) SongOption {
	return func(s *songItem) {
		s.loudness = f
	}
}

// Duration lets the player know how long it should expect the song to be.
func Duration(d time.Duration) SongOption {
	return func(s *songItem) {
		s.duration = d
	}
}

func OnStart(f func()) SongOption {
	return func(s *songItem) {
		if f != nil {
			s.onStart = f
		}
	}
}

// OnEnd
func OnEnd(f func(elapsed time.Duration, err error)) SongOption {
	return func(s *songItem) {
		if f != nil {
			s.onEnd = f
		}
	}
}

// OnProgress interval is approximate, will be quantized to a multiple of frame duration.
func OnProgress(f func(elapsed time.Duration, frameTime []time.Time), interval time.Duration) SongOption {
	return func(s *songItem) {
		if f != nil {
			s.onProgress = f
			s.progressInterval = interval
		}
	}
}

func OnPause(f func(elapsed time.Duration)) SongOption {
	return func(s *songItem) {
		if f != nil {
			s.onPause = f
		}
	}
}

func OnResume(f func(elapsed time.Duration)) SongOption {
	return func(s *songItem) {
		if f != nil {
			s.onResume = f
		}
	}
}
