package bot

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

const antiDeleteCacheMax = 800

type cachedMessage struct {
	Chat      types.JID
	Sender    types.JID
	ID        string
	Message   *proto.Message
	Text      string
	MediaKind string
	At        time.Time
}

func (b *Bot) cacheMessage(evt *events.Message) {
	if evt == nil || evt.Message == nil || evt.Info.ID == "" {
		return
	}
	if !b.isAntiDeleteEnabled(evt.Info.Chat) {
		return
	}
	// Jangan cache protocol/revoke message.
	if evt.Message.GetProtocolMessage() != nil {
		return
	}
	cm := cachedMessage{
		Chat:      evt.Info.Chat,
		Sender:    evt.Info.Sender,
		ID:        evt.Info.ID,
		Message:   evt.Message,
		Text:      extractText(evt.Message),
		MediaKind: mediaKind(evt.Message),
		At:        time.Now(),
	}
	key := messageCacheKey(cm.Chat, cm.ID)

	b.deletedMu.Lock()
	defer b.deletedMu.Unlock()
	if _, exists := b.msgCache[key]; !exists {
		b.msgOrder = append(b.msgOrder, key)
	}
	b.msgCache[key] = cm
	for len(b.msgOrder) > antiDeleteCacheMax {
		old := b.msgOrder[0]
		b.msgOrder = b.msgOrder[1:]
		delete(b.msgCache, old)
	}
}

func (b *Bot) handleAntiDelete(ctx context.Context, evt *events.Message) bool {
	if !b.isAntiDeleteEnabled(evt.Info.Chat) {
		return false
	}
	pm := evt.Message.GetProtocolMessage()
	if pm == nil || pm.GetType() != proto.ProtocolMessage_REVOKE {
		return false
	}
	key := pm.GetKey()
	id := key.GetID()
	remote := key.GetRemoteJID()
	chat := evt.Info.Chat
	if remote != "" {
		if parsed, err := types.ParseJID(remote); err == nil && !parsed.IsEmpty() {
			chat = parsed
		}
	}

	cacheKey := messageCacheKey(chat, id)
	b.deletedMu.Lock()
	cm, ok := b.msgCache[cacheKey]
	b.deletedMu.Unlock()
	if !ok {
		b.sendText(ctx, evt.Info.Chat, "🚫 Pesan dihapus, tapi belum ada di cache anti-delete.")
		return true
	}

	senderMention := cm.Sender.String()
	caption := fmt.Sprintf("🚫 *Anti-delete*\nDihapus oleh: @%s\nPengirim asli: @%s", mentionName(evt.Info.Sender.String()), mentionName(senderMention))
	mentions := []string{evt.Info.Sender.String(), senderMention}
	if strings.TrimSpace(cm.Text) != "" {
		caption += "\n\nPesan:\n" + cm.Text
	}

	if dl, kind := downloadableFromMessage(cm.Message); dl != nil {
		data, err := b.client.Download(ctx, dl)
		if err == nil && len(data) > 0 {
			mime := mediaMime(cm.Message, kind)
			b.sendMedia(ctx, evt.Info.Chat, data, mime, caption)
			return true
		}
		log.Println("anti-delete media download error:", err)
	}
	b.sendMentionText(ctx, evt.Info.Chat, caption, mentions)
	return true
}

func messageCacheKey(chat types.JID, id string) string {
	return chat.String() + "|" + id
}

func mediaKind(m *proto.Message) string {
	if m.GetImageMessage() != nil {
		return "image"
	}
	if m.GetVideoMessage() != nil {
		return "video"
	}
	if m.GetAudioMessage() != nil {
		return "audio"
	}
	if m.GetDocumentMessage() != nil {
		return "document"
	}
	if m.GetStickerMessage() != nil {
		return "sticker"
	}
	return ""
}

func downloadableFromMessage(m *proto.Message) (whatsmeow.DownloadableMessage, string) {
	if im := m.GetImageMessage(); im != nil {
		return im, "image"
	}
	if vm := m.GetVideoMessage(); vm != nil {
		return vm, "video"
	}
	if am := m.GetAudioMessage(); am != nil {
		return am, "audio"
	}
	if dm := m.GetDocumentMessage(); dm != nil {
		return dm, "document"
	}
	if sm := m.GetStickerMessage(); sm != nil {
		return sm, "sticker"
	}
	return nil, ""
}

func mediaMime(m *proto.Message, kind string) string {
	switch kind {
	case "image":
		if v := m.GetImageMessage().GetMimetype(); v != "" {
			return v
		}
		return "image/jpeg"
	case "video":
		if v := m.GetVideoMessage().GetMimetype(); v != "" {
			return v
		}
		return "video/mp4"
	case "audio":
		if v := m.GetAudioMessage().GetMimetype(); v != "" {
			return v
		}
		return "audio/ogg"
	case "document":
		if v := m.GetDocumentMessage().GetMimetype(); v != "" {
			return v
		}
		return "application/octet-stream"
	case "sticker":
		return "image/webp"
	default:
		return "application/octet-stream"
	}
}
