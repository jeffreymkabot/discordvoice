package discordvoice

import (
	"io"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/pkg/errors"
)

// Opener
type Opener interface {
	Open(channelID string) (Writer, error)
}

type Writer interface {
	io.WriteCloser
	Speaking(bool) error
}

// DiscordOpener is a synchronous implementation of the Opener interface.
// TODO this impl could be a subpackage, really
type DiscordOpener struct {
	guildID     string
	sendTimeout time.Duration
	discord     *discordgo.Session
	mu          sync.Mutex
	writer      *discordWriter
}

func NewDiscordOpener(discord *discordgo.Session, guildID string, sendTimeout time.Duration) *DiscordOpener {
	return &DiscordOpener{
		guildID:     guildID,
		sendTimeout: sendTimeout,
		discord:     discord,
	}
}

// Open
// Open will recycle the previous Writer if it is still open to the same channel.
func (o *DiscordOpener) Open(channelID string) (Writer, error) {
	if !validVoiceChannel(o.discord, channelID) {
		return nil, ErrInvalidVoiceChannel
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.writer == nil || o.writer.channelID != channelID || !o.writer.ready() {
		vconn, err := o.discord.ChannelVoiceJoin(o.guildID, channelID, false, true)
		if err != nil {
			o.writer = nil
			return nil, errors.Wrap(err, "failed to join discord channel")
		}
		o.writer = &discordWriter{channelID, o.sendTimeout, vconn}
	}
	return o.writer, nil
}

// discordWriter implements Writer.
type discordWriter struct {
	channelID   string
	sendTimeout time.Duration
	vconn       *discordgo.VoiceConnection
}

func (w *discordWriter) ready() bool {
	w.vconn.RLock()
	defer w.vconn.RUnlock()
	// check that the channel hasn't changed under our nose
	// e.g. a user dragging us into a different channel?
	return w.vconn.ChannelID == w.channelID && w.vconn.Ready
}

func (w *discordWriter) Write(p []byte) (n int, err error) {
	if !w.ready() {
		err = errors.New("voice connection closed")
		return
	}
	select {
	case w.vconn.OpusSend <- p:
		n = len(p)
		return
	case <-time.After(w.sendTimeout):
		err = errors.Errorf("send timeout on voice connection after %vms", w.sendTimeout)
		return
	}
}

func (w *discordWriter) Close() error {
	return w.vconn.Disconnect()
}

func (w *discordWriter) Speaking(b bool) error {
	return w.vconn.Speaking(b)
}

func validVoiceChannel(discord *discordgo.Session, channelID string) bool {
	channel, err := discord.State.Channel(channelID)
	if err != nil {
		channel, err = discord.Channel(channelID)
	}
	return err == nil && channel.Type == discordgo.ChannelTypeGuildVoice
}
