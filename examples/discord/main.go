package main

import (
	"flag"
	"io"
	"log"
	"os"
	"os/signal"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/jeffreymkabot/discordvoice"
	"github.com/jeffreymkabot/discordvoice/discordvoice"
	"github.com/jonas747/dca"
)

func main() {
	tok := flag.String("t", "", "discord token")
	guildID := flag.String("g", "", "guild ID")
	channelID := flag.String("c", "", "channel ID")
	flag.Parse()

	session, err := discordgo.New("Bot " + *tok)
	if err != nil {
		log.Fatal(err)
	}

	err = session.Open()
	if err != nil {
		log.Fatal(err)
	}

	device := discordvoice.New(session, *guildID, 1*time.Second)
	openDevice := func() (io.Writer, error) {
		return device.Open(*channelID)
	}
	openSource := func() (player.Source, error) {
		f, err := os.Open("media/test_file.mp3")
		if err != nil {
			return nil, err
		}
		return discordvoice.NewSource(f, dca.StdEncodeOptions)
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
		}))

	select {
	case <-end:
	case <-sig:
	case <-time.After(10 * time.Second):
	}
}
