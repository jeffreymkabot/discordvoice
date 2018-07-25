package discordvoice

import (
	"io"
	"log"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/pkg/errors"
)

var ErrInvalidVoiceChannel = errors.New("invalid voice channel")

const initRetries = 3

// Device
type Device struct {
	guildID     string
	sendTimeout time.Duration
	discord     *discordgo.Session
	mu          sync.Mutex
	writer      *Writer
}

func New(discord *discordgo.Session, guildID string, sendTimeout time.Duration) *Device {
	return &Device{
		guildID:     guildID,
		sendTimeout: sendTimeout,
		discord:     discord,
	}
}

// Open produces an io.Writer interface for sending audio frames to a discord voice channel.
// Open will recycle the previous Writer if it is still open to the same channel.
func (d *Device) Open(channelID string) (io.Writer, error) {
	if !ValidVoiceChannel(d.discord, channelID) {
		return nil, ErrInvalidVoiceChannel
	}
	d.mu.Lock()
	if d.writer != nil && d.writer.channelID != channelID {
		d.writer.Close()
		d.writer = nil
	}
	if d.writer == nil {
		vconn, err := d.discord.ChannelVoiceJoin(d.guildID, channelID, false, true)
		if err != nil {
			return nil, errors.Wrap(err, "failed to join discord channel")
		}
		d.writer = &Writer{
			guildID:     d.guildID,
			channelID:   channelID,
			sendTimeout: d.sendTimeout,
			discord:     d.discord,
			vconn:       vconn,
		}
	}
	defer d.mu.Unlock()
	d.writer.vconn.Speaking(true)
	return d.writer, nil
}

// Writer
type Writer struct {
	guildID     string
	channelID   string
	sendTimeout time.Duration
	discord     *discordgo.Session
	vconn       *discordgo.VoiceConnection
}

// TODO writer intelligently calls vconn.Speaking(true/false) before/after writing
func (w *Writer) Write(p []byte) (n int, err error) {
	return w.tryWrite(p, initRetries)
}

// TODO provide a way to break out of retry loop when player gets closed
func (w *Writer) tryWrite(p []byte, retries int) (n int, err error) {
	select {
	case w.vconn.OpusSend <- p:
		return len(p), nil
	case <-time.After(w.sendTimeout):
		if retries <= 0 {
			err = errors.New("failed to write to voice connection")
			return 0, err
		}
		log.Printf("send timeout, reconnecting, retries left: %v", retries)
		w.Close()
		time.Sleep(1 * time.Second)
		vconn, err := w.discord.ChannelVoiceJoin(w.guildID, w.channelID, false, true)
		if err != nil {
			return 0, errors.Wrap(err, "failed to join discord channel")
		}
		w.vconn = vconn
		w.vconn.Speaking(true)
		return w.tryWrite(p, retries-1)
	}
}

func (w *Writer) Close() error {
	w.vconn.Speaking(false)
	err := w.vconn.Disconnect()
	w.vconn = nil
	return err
}

func ValidVoiceChannel(discord *discordgo.Session, channelID string) bool {
	channel, err := discord.State.Channel(channelID)
	if err != nil {
		channel, err = discord.Channel(channelID)
	}
	if err != nil {
		return false
	}
	// add this channel to State to speed up next query
	discord.State.ChannelAdd(channel)
	return channel.Type == discordgo.ChannelTypeGuildVoice
}
