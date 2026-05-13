package bot

import (
	"context"
	"fmt"
	"log"
	"strings"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

func (b *Bot) handleViewOnceCommand(ctx context.Context, chat types.JID, text string) {
	args := strings.Fields(strings.ToLower(text))
	if len(args) < 2 {
		status := "mati ❌"
		if b.isViewOnceEnabled(chat) {
			status = "aktif ✅"
		}
		b.sendText(ctx, chat, "View once auto-save: "+status+"\n\nFormat:\n"+b.cfg.Prefix+"viewonce on\n"+b.cfg.Prefix+"viewonce off\n"+b.cfg.Prefix+"viewonce status\n\nUntuk lihat manual: reply media sekali lihat lalu ketik "+b.cfg.Prefix+"l")
		return
	}

	switch args[1] {
	case "on", "enable", "aktif":
		b.setViewOnce(chat, true)
		b.sendText(ctx, chat, "View once saver diaktifkan ✅\nFoto/video sekali lihat akan dikirim ulang sebagai media biasa di chat ini.")
	case "off", "disable", "mati":
		b.setViewOnce(chat, false)
		b.sendText(ctx, chat, "View once saver dimatikan ✅")
	case "status":
		if b.isViewOnceEnabled(chat) {
			b.sendText(ctx, chat, "View once saver aktif ✅")
		} else {
			b.sendText(ctx, chat, "View once saver mati ❌")
		}
	default:
		b.sendText(ctx, chat, "Format:\n"+b.cfg.Prefix+"viewonce on\n"+b.cfg.Prefix+"viewonce off\n"+b.cfg.Prefix+"viewonce status")
	}
}

func (b *Bot) handleLookViewOnce(ctx context.Context, evt *events.Message) {
	if evt == nil {
		return
	}
	chat := evt.Info.Chat
	mediaMsg, kind, captionText := lookViewOnceTarget(evt.Message)
	if mediaMsg == nil {
		b.sendText(ctx, chat, "Reply foto/video sekali lihat lalu ketik "+b.cfg.Prefix+"l")
		return
	}

	b.react(ctx, evt, "⏳")
	data, err := b.client.Download(ctx, mediaMsg)
	if err != nil || len(data) == 0 {
		log.Println("look view once download error:", err)
		b.sendText(ctx, chat, "❌ Gagal lihat media. Kemungkinan media sekali lihatnya sudah kadaluarsa/terbuka.")
		b.react(ctx, evt, "❌")
		return
	}

	caption := "👁️ *Lihat sekali*"
	if captionText != "" {
		caption += "\n\nCaption:\n" + captionText
	}
	b.sendMedia(ctx, chat, data, mediaMimeFromKind(mediaMsg, kind), caption)
	b.react(ctx, evt, "✅")
}

func (b *Bot) handleViewOnce(ctx context.Context, evt *events.Message) bool {
	if evt == nil || evt.Message == nil || !b.isViewOnceMessage(evt) {
		return false
	}
	if !b.isViewOnceEnabled(evt.Info.Chat) {
		return false
	}

	mediaMsg, kind := viewOnceMedia(evt.Message)
	if mediaMsg == nil {
		return false
	}

	data, err := b.client.Download(ctx, mediaMsg)
	if err != nil || len(data) == 0 {
		log.Println("view once media download error:", err)
		b.sendText(ctx, evt.Info.Chat, "👁️ Ada media sekali lihat, tapi gagal didownload.")
		return true
	}

	caption := fmt.Sprintf("👁️ *View once tersimpan*\nPengirim: @%s", mentionName(evt.Info.Sender.String()))
	if txt := strings.TrimSpace(extractText(evt.Message)); txt != "" {
		caption += "\n\nCaption:\n" + txt
	}

	b.sendMedia(ctx, evt.Info.Chat, data, mediaMimeFromKind(mediaMsg, kind), caption)
	return true
}

func (b *Bot) isViewOnceMessage(evt *events.Message) bool {
	if evt == nil || evt.Message == nil {
		return false
	}
	if evt.UnavailableRequestID != "" && b.isViewOnceEnabled(evt.Info.Chat) {
		if dl, _ := viewOnceMedia(evt.Message); dl != nil {
			log.Printf("👁️ treating unavailable response as view once chat=%s sender=%s msg=%s request=%s", evt.Info.Chat.String(), evt.Info.Sender.String(), evt.Info.ID, evt.UnavailableRequestID)
			return true
		}
	}
	if evt.IsViewOnce || evt.RawMessage.GetViewOnceMessage().GetMessage() != nil || evt.RawMessage.GetViewOnceMessageV2().GetMessage() != nil || evt.RawMessage.GetViewOnceMessageV2Extension().GetMessage() != nil {
		return true
	}
	m := unwrapViewOnceMessage(evt.Message)
	if im := m.GetImageMessage(); im != nil && im.GetViewOnce() {
		return true
	}
	if vm := m.GetVideoMessage(); vm != nil && vm.GetViewOnce() {
		return true
	}
	return false
}

func viewOnceMedia(m *proto.Message) (whatsmeow.DownloadableMessage, string) {
	m = unwrapViewOnceMessage(m)
	if m == nil {
		return nil, ""
	}
	if im := m.GetImageMessage(); im != nil {
		return im, "image"
	}
	if vm := m.GetVideoMessage(); vm != nil {
		return vm, "video"
	}
	return nil, ""
}

func lookViewOnceTarget(m *proto.Message) (whatsmeow.DownloadableMessage, string, string) {
	if m == nil {
		return nil, "", ""
	}
	if em := m.GetExtendedTextMessage(); em != nil && em.GetContextInfo() != nil && em.GetContextInfo().GetQuotedMessage() != nil {
		m = em.GetContextInfo().GetQuotedMessage()
	}
	m = unwrapViewOnceMessage(m)
	dl, kind := viewOnceMedia(m)
	return dl, kind, strings.TrimSpace(extractText(m))
}

func unwrapViewOnceMessage(m *proto.Message) *proto.Message {
	if m == nil {
		return nil
	}
	if inner := m.GetViewOnceMessage().GetMessage(); inner != nil {
		return inner
	}
	if inner := m.GetViewOnceMessageV2().GetMessage(); inner != nil {
		return inner
	}
	if inner := m.GetViewOnceMessageV2Extension().GetMessage(); inner != nil {
		return inner
	}
	return m
}

func mediaMimeFromKind(dl whatsmeow.DownloadableMessage, kind string) string {
	switch v := dl.(type) {
	case *proto.ImageMessage:
		if mt := v.GetMimetype(); mt != "" {
			return mt
		}
		return "image/jpeg"
	case *proto.VideoMessage:
		if mt := v.GetMimetype(); mt != "" {
			return mt
		}
		return "video/mp4"
	default:
		if kind == "video" {
			return "video/mp4"
		}
		return "image/jpeg"
	}
}
