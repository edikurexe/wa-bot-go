package bot

import (
	"context"
	"strings"

	"go.mau.fi/whatsmeow/types"
)

func (b *Bot) handleAntiDeleteCommand(ctx context.Context, chat types.JID, text string) {
	parts := strings.Fields(strings.ToLower(text))
	if len(parts) < 2 || parts[1] == "status" {
		if b.isAntiDeleteEnabled(chat) {
			b.sendText(ctx, chat, "Anti-delete: ON ✅")
		} else {
			b.sendText(ctx, chat, "Anti-delete: OFF")
		}
		return
	}

	switch parts[1] {
	case "on", "enable", "aktif":
		b.setAntiDelete(chat, true)
		b.sendText(ctx, chat, "Anti-delete diaktifkan ✅\nPesan yang dihapus akan dicoba repost oleh bot.")
	case "off", "disable", "mati":
		b.setAntiDelete(chat, false)
		b.clearAntiDeleteCache(chat)
		b.sendText(ctx, chat, "Anti-delete dimatikan ✅")
	default:
		b.sendText(ctx, chat, "Format:\n.antidelete on\n.antidelete off\n.antidelete status")
	}
}

func (b *Bot) clearAntiDeleteCache(chat types.JID) {
	prefix := chat.String() + "|"
	b.deletedMu.Lock()
	defer b.deletedMu.Unlock()
	for k := range b.msgCache {
		if strings.HasPrefix(k, prefix) {
			delete(b.msgCache, k)
		}
	}
	filtered := b.msgOrder[:0]
	for _, k := range b.msgOrder {
		if !strings.HasPrefix(k, prefix) {
			filtered = append(filtered, k)
		}
	}
	b.msgOrder = filtered
}
