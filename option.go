package player

import "time"

// PlayerConfig sets some behaviors of the Player.
type PlayerConfig struct {
	QueueLength int
	Idle        func()
	IdleTimeout int
}

// PlayerOptions set up the PlayerConfig.
type PlayerOption func(*PlayerConfig)

// QueueLength is the maximum number of items that will be allowed in the player queue.
// Values less than 1 allow an unbounded queue.
func QueueLength(n int) PlayerOption {
	return func(cfg *PlayerConfig) {
		cfg.QueueLength = n
	}
}

// IdleFunc sets a function that is called if the player does not receive another item for d milliseconds.
func IdleFunc(idle func(), d int) PlayerOption {
	return func(cfg *PlayerConfig) {
		if d > 0 && idle != nil {
			cfg.Idle = idle
			cfg.IdleTimeout = d
		}
	}
}

// SongOptions set up the playback of individual items.
type SongOption func(*songItem)

// PreEncoded causes the item not to be passed through ffmpeg for playback.
func PreEncoded() SongOption {
	return func(s *songItem) {
		s.preencoded = true
	}
}

// Filter sets the ffmpeg audio filter string.  Filter has no effect if the item is PreEncoded.
func Filter(af string) SongOption {
	return func(s *songItem) {
		s.filters = af
	}
}

// Loudness sets the encoder's loudness target.  Higher is louder.
// See https://ffmpeg.org/ffmpeg-filters.html#loudnorm.
// Values less than -70.0 or greater than -5.0 have no effect.
// In particular, the default value of 0 has no effect and input loudness will be unchanged.
func Loudness(f float64) SongOption {
	return func(s *songItem) {
		s.loudness = f
	}
}

// Duration lets the player know how long it should expect the item's playback to be.
func Duration(d time.Duration) SongOption {
	return func(s *songItem) {
		s.duration = d
	}
}

// OnStart sets a function that is called when the item's playback begins.
func OnStart(f func()) SongOption {
	return func(s *songItem) {
		if f != nil {
			s.onStart = f
		}
	}
}

// OnEnd sets a function that is called when the item's playback ends.
// The callback receives how long the item played and an error detailing why the playback ended.
// The error is never nil.
func OnEnd(f func(elapsed time.Duration, err error)) SongOption {
	return func(s *songItem) {
		if f != nil {
			s.onEnd = f
		}
	}
}

// OnProgress sets a function called periodically during the item's playback.
// The callback receives how long the item has played and a slice frame-to-frame latencies.
func OnProgress(f func(elapsed time.Duration, frameTime []time.Time), interval time.Duration) SongOption {
	return func(s *songItem) {
		if f != nil {
			s.onProgress = f
			s.progressInterval = interval
		}
	}
}

// OnPause sets a function called when the item's playback pauses.
// The callback receives how long the item has played
func OnPause(f func(elapsed time.Duration)) SongOption {
	return func(s *songItem) {
		if f != nil {
			s.onPause = f
		}
	}
}

// OnResume sets a function called when the item's playback resumes.
// The callback receives how long the item has played
func OnResume(f func(elapsed time.Duration)) SongOption {
	return func(s *songItem) {
		if f != nil {
			s.onResume = f
		}
	}
}
