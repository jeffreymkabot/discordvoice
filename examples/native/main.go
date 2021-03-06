package main

import (
	"io"
	"log"
	"os"
	"os/signal"
	"time"

	"github.com/hajimehoshi/oto"
	"github.com/jeffreymkabot/discordvoice"
	"github.com/jeffreymkabot/discordvoice/mp3"
)

func main() {
	openSource := func() (player.Source, error) {
		f, err := os.Open("media/test_file.mp3")
		if err != nil {
			return nil, err
		}
		return mp3.NewSource(f)
	}

	bufferSize := 1 << 15
	openDevice := func() (io.Writer, error) {
		return oto.NewPlayer(44100, 2, 2, bufferSize)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, os.Kill)
	end := make(chan struct{})

	p := player.New()
	defer p.Close()
	p.Enqueue("test", openSource, openDevice,
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
