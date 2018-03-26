package discordvoice

import (
	"io"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/pkg/errors"
)

var ErrInvalidVoiceChannel = errors.New("invalid voice channel")

// WriterOpener
type WriterOpener struct {
	guildID     string
	sendTimeout time.Duration
	discord     *discordgo.Session
	mu          sync.Mutex
	writer      *Writer
}

func NewWriterOpener(discord *discordgo.Session, guildID string, sendTimeout time.Duration) *WriterOpener {
	return &WriterOpener{
		guildID:     guildID,
		sendTimeout: sendTimeout,
		discord:     discord,
	}
}

// Open implements the player.WriterOpener interface.
// Open will recycle the previous Writer if it is still open to the same channel.
func (o *WriterOpener) Open(channelID string) (io.WriteCloser, error) {
	if !ValidVoiceChannel(o.discord, channelID) {
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
		o.writer = &Writer{channelID, o.sendTimeout, vconn}
	}
	o.writer.vconn.Speaking(true)
	return o.writer, nil
}

// Writer
type Writer struct {
	channelID   string
	sendTimeout time.Duration
	vconn       *discordgo.VoiceConnection
}

func (w *Writer) ready() bool {
	w.vconn.RLock()
	defer w.vconn.RUnlock()
	// check that the channel hasn't changed under our nose
	// e.g. a user dragging us into a different channel?
	return w.vconn.ChannelID == w.channelID && w.vconn.Ready
}

// TODO writer intelligently calls vconn.Speaking(true/false) before/after writing
func (w *Writer) Write(p []byte) (n int, err error) {
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

func (w *Writer) Close() error {
	w.vconn.Speaking(false)
	return w.vconn.Disconnect()
}

func ValidVoiceChannel(discord *discordgo.Session, channelID string) bool {
	channel, err := discord.State.Channel(channelID)
	if err != nil {
		channel, err = discord.Channel(channelID)
	}
	return err == nil && channel.Type == discordgo.ChannelTypeGuildVoice
}
