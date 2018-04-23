package discordvoice

import (
	"io"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/pkg/errors"
)

var ErrInvalidVoiceChannel = errors.New("invalid voice channel")

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

// Open implements the player.Device interface.
// Open will recycle the previous Writer if it is still open to the same channel.
func (d *Device) Open(channelID string) (io.WriteCloser, error) {
	if !ValidVoiceChannel(d.discord, channelID) {
		return nil, ErrInvalidVoiceChannel
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.writer == nil || d.writer.channelID != channelID || !d.writer.Ready() {
		vconn, err := d.discord.ChannelVoiceJoin(d.guildID, channelID, false, true)
		if err != nil {
			d.writer = nil
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
	d.writer.vconn.Speaking(true)
	return d.writer, nil
}

// Writer
type Writer struct {
	guildID     string
	channelID   string
	sendTimeout time.Duration
	discord     *discordgo.Session
	mu          sync.Mutex
	vconn       *discordgo.VoiceConnection
}

func (w *Writer) Ready() bool {
	w.vconn.RWMutex.RLock()
	defer w.vconn.RWMutex.RUnlock()
	return w.ready()
}

// check that the channel hasn't changed under our nose
// e.g. websocket error or a user dragging us into a different channel?
func (w *Writer) ready() bool {
	return w.vconn.ChannelID == w.channelID && w.vconn.Ready
}

// TODO writer intelligently calls vconn.Speaking(true/false) before/after writing
func (w *Writer) Write(p []byte) (n int, err error) {
	if !w.Ready() {
		// TODO attempt reconnect, could just skip checking ready and let the channel send timeout
		err = errors.New("voice connection closed")
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.write(p, true)
}

func (w *Writer) write(p []byte, retryOnTimeout bool) (n int, err error) {
	select {
	case w.vconn.OpusSend <- p:
		return len(p), nil
	case <-time.After(w.sendTimeout):
		if !retryOnTimeout {
			err = errors.Errorf("send timeout on voice connection after %v", w.sendTimeout)
			return 0, err
		}
		vconn, err := w.reconnect()
		if err != nil {
			return 0, err
		}
		w.vconn = vconn
		return w.write(p, false)
	}
}

func (w *Writer) reconnect() (*discordgo.VoiceConnection, error) {
	w.vconn.Disconnect()
	return w.discord.ChannelVoiceJoin(w.guildID, w.channelID, false, true)
}

func (w *Writer) Close() error {
	w.vconn.Speaking(false)
	return w.vconn.Disconnect()
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
