package main

import (
	"io"
	"log"
	"os"
	"os/signal"
	"time"

	"github.com/hajimehoshi/go-mp3"
	"github.com/hajimehoshi/oto"
	"github.com/jeffreymkabot/discordvoice"
)

// go-mp3 constants
const (
	bytesPerSample = 4
	// from github.com/hajimehoshi/go-mp3/internal/consts
	bytesPerFrame = 4608
)

type mp3Source struct {
	decoder *mp3.Decoder
}

func (src mp3Source) ReadFrame() (frame []byte, err error) {
	frame = make([]byte, bytesPerFrame)
	nr, err := src.decoder.Read(frame)
	frame = frame[0:nr]
	return
}

func (src mp3Source) FrameDuration() time.Duration {
	bytesPerSecond := bytesPerSample * src.decoder.SampleRate()
	secondsPerFrame := float64(bytesPerFrame) / float64(bytesPerSecond)
	return time.Duration(secondsPerFrame * float64(time.Second))
}

func main() {
	p := player.New()
	defer p.Close()

	openSong := func() (io.Reader, error) {
		return os.Open("test.mp3")
	}
	encodeSong := func(r io.Reader) (player.Source, error) {
		decoder, err := mp3.NewDecoder(r.(io.ReadCloser))
		if err != nil {
			return nil, err
		}
		return mp3Source{decoder}, nil
	}
	bufferSize := 1 << 15
	openDevice := func() (io.Writer, error) {
		return oto.NewPlayer(44100, 2, 2, bufferSize)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, os.Kill)
	end := make(chan struct{})

	p.Enqueue("test", openSong, openDevice,
		player.Encoder(encodeSong),
		player.OnStart(func() {
			log.Print("playback started")
		}),
		player.OnProgress(func(e time.Duration, latencies []time.Duration) {
			log.Printf("played %v seconds", e)
		}, 1*time.Second),
		player.OnEnd(func(e time.Duration, err error) {
			log.Printf("playback stopped after %v because %v", e, err)
			close(end)
		}),
	)

	select {
	case <-end:
	case <-sig:
	case <-time.After(10 * time.Second):
	}
}
