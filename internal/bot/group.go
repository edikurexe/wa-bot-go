package bot

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/types"
)

var phoneMentionRE = regexp.MustCompile(`\d{6,}`)

func (b *Bot) handleGroupCommand(ctx context.Context, chat types.JID, sender types.JID, msg *proto.Message, text, lower string) bool {
	p := b.cfg.Prefix
	isGroupCommand := lower == p+"group" || lower == p+"groupmenu" ||
		lower == p+"tagall" || strings.HasPrefix(lower, p+"tagall ") ||
		lower == p+"hidetag" || strings.HasPrefix(lower, p+"hidetag ") || lower == p+"h" || strings.HasPrefix(lower, p+"h ") ||
		lower == p+"open" || lower == p+"buka" || lower == p+"close" || lower == p+"tutup" ||
		lower == p+"antidelete" || strings.HasPrefix(lower, p+"antidelete ") || lower == p+"antidel" || strings.HasPrefix(lower, p+"antidel ") ||
		lower == p+"linkgc" || lower == p+"linkgroup" || lower == p+"resetlink" ||
		lower == p+"kick" || strings.HasPrefix(lower, p+"kick ") || lower == p+"k" || strings.HasPrefix(lower, p+"k ") ||
		lower == p+"promote" || strings.HasPrefix(lower, p+"promote ") ||
		lower == p+"demote" || strings.HasPrefix(lower, p+"demote ")
	if !isGroupCommand {
		return false
	}
	if chat.Server != types.GroupServer {
		b.sendText(ctx, chat, "Command ini khusus grup.")
		return true
	}

	switch {
	case lower == p+"group" || lower == p+"groupmenu":
		b.sendText(ctx, chat, groupMenuText(p))
	case lower == p+"tagall" || lower == p+"tagall " || strings.HasPrefix(lower, p+"tagall "):
		note := strings.TrimSpace(text[len(p+"tagall"):])
		b.tagAll(ctx, chat, note, false)
	case lower == p+"hidetag" || lower == p+"h" || strings.HasPrefix(lower, p+"hidetag "):
		note := strings.TrimSpace(strings.TrimPrefix(text, p+"hidetag"))
		if strings.HasPrefix(lower, p+"h ") {
			note = strings.TrimSpace(text[len(p+"h"):])
		}
		b.tagAll(ctx, chat, note, true)
	case lower == p+"open" || lower == p+"buka":
		b.setAnnounce(ctx, chat, false)
	case lower == p+"close" || lower == p+"tutup":
		b.setAnnounce(ctx, chat, true)
	case lower == p+"antidelete" || strings.HasPrefix(lower, p+"antidelete ") || lower == p+"antidel" || strings.HasPrefix(lower, p+"antidel "):
		b.handleAntiDeleteCommand(ctx, chat, text)
	case lower == p+"linkgc" || lower == p+"linkgroup":
		b.groupLink(ctx, chat, false)
	case lower == p+"resetlink":
		b.groupLink(ctx, chat, true)
	case lower == p+"kick" || strings.HasPrefix(lower, p+"kick ") || lower == p+"k" || strings.HasPrefix(lower, p+"k "):
		b.participantAction(ctx, chat, msg, text, p, whatsmeow.ParticipantChangeRemove, "kick")
	case lower == p+"promote" || strings.HasPrefix(lower, p+"promote "):
		b.participantAction(ctx, chat, msg, text, p, whatsmeow.ParticipantChangePromote, "promote")
	case lower == p+"demote" || strings.HasPrefix(lower, p+"demote "):
		b.participantAction(ctx, chat, msg, text, p, whatsmeow.ParticipantChangeDemote, "demote")
	default:
		return false
	}
	_ = sender // reserved for later admin checks
	return true
}

func groupMenuText(p string) string {
	return "╭─〔 🛡️ *GROUP MANAGEMENT* 〕\n" +
		"│ " + p + "tagall <teks>\n" +
		"│   mention semua member\n" +
		"│\n" +
		"│ " + p + "hidetag <teks>\n" +
		"│   mention silent\n" +
		"│\n" +
		"│ " + p + "open / " + p + "close\n" +
		"│   buka/tutup grup\n" +
		"│\n" +
		"│ " + p + "kick @user\n" +
		"│ " + p + "promote @user\n" +
		"│ " + p + "demote @user\n" +
		"│\n" +
		"│ " + p + "linkgc\n" +
		"│ " + p + "resetlink\n" +
		"│\n" +
		"│ " + p + "antidelete on\n" +
		"│ " + p + "antidelete off\n" +
		"│ " + p + "antidelete status\n" +
		"╰───────────────\n\n" +
		"_Bot harus admin untuk fitur kontrol grup._"
}

func (b *Bot) tagAll(ctx context.Context, chat types.JID, note string, hidden bool) {
	info, err := b.client.GetGroupInfo(ctx, chat)
	if err != nil {
		b.sendText(ctx, chat, "❌ Gagal ambil data grup.")
		return
	}
	mentions := make([]string, 0, len(info.Participants))
	var body strings.Builder
	if note == "" {
		note = "Tag all"
	}
	body.WriteString(note)
	if !hidden {
		body.WriteString("\n\n")
	}
	for i, p := range info.Participants {
		jid := participantMentionJID(p)
		if jid == "" {
			continue
		}
		mentions = append(mentions, jid)
		if !hidden {
			body.WriteString(fmt.Sprintf("%d. @%s\n", i+1, mentionName(jid)))
		}
	}
	b.sendMentionText(ctx, chat, strings.TrimSpace(body.String()), mentions)
}

func participantMentionJID(p types.GroupParticipant) string {
	if !p.PhoneNumber.IsEmpty() {
		return p.PhoneNumber.String()
	}
	if !p.JID.IsEmpty() {
		return p.JID.String()
	}
	if !p.LID.IsEmpty() {
		return p.LID.String()
	}
	return ""
}

func mentionName(jid string) string {
	return strings.Split(jid, "@")[0]
}

func (b *Bot) sendMentionText(ctx context.Context, chat types.JID, text string, mentions []string) {
	_, err := b.client.SendMessage(ctx, chat, &proto.Message{ExtendedTextMessage: &proto.ExtendedTextMessage{
		Text: &text,
		ContextInfo: &proto.ContextInfo{
			MentionedJID: mentions,
		},
	}})
	if err != nil {
		b.sendText(ctx, chat, "❌ Gagal kirim mention.")
	}
}

func (b *Bot) setAnnounce(ctx context.Context, chat types.JID, announce bool) {
	if err := b.client.SetGroupAnnounce(ctx, chat, announce); err != nil {
		b.sendText(ctx, chat, "❌ Gagal ubah setting grup. Pastikan bot admin.")
		return
	}
	if announce {
		b.sendText(ctx, chat, "Grup ditutup ✅ hanya admin yang bisa chat.")
	} else {
		b.sendText(ctx, chat, "Grup dibuka ✅ semua member bisa chat.")
	}
}

func (b *Bot) groupLink(ctx context.Context, chat types.JID, reset bool) {
	link, err := b.client.GetGroupInviteLink(ctx, chat, reset)
	if err != nil {
		b.sendText(ctx, chat, "❌ Gagal ambil link grup. Pastikan bot admin.")
		return
	}
	b.sendText(ctx, chat, link)
}

func (b *Bot) participantAction(ctx context.Context, chat types.JID, msg *proto.Message, text, prefix string, action whatsmeow.ParticipantChange, label string) {
	targets := extractTargets(msg, text)
	if len(targets) == 0 {
		b.sendText(ctx, chat, "Reply/mention target. Contoh: "+prefix+label+" @user")
		return
	}
	_, err := b.client.UpdateGroupParticipants(ctx, chat, targets, action)
	if err != nil {
		b.sendText(ctx, chat, "❌ Gagal "+label+" target. Pastikan bot admin dan target valid.")
		return
	}
	b.sendText(ctx, chat, "✅ Berhasil "+label+" target.")
}

func extractTargets(msg *proto.Message, text string) []types.JID {
	seen := map[string]bool{}
	var out []types.JID
	add := func(j types.JID) {
		if j.IsEmpty() || seen[j.String()] {
			return
		}
		seen[j.String()] = true
		out = append(out, j)
	}
	if em := msg.GetExtendedTextMessage(); em != nil && em.ContextInfo != nil {
		for _, s := range em.ContextInfo.MentionedJID {
			if j, err := types.ParseJID(s); err == nil {
				add(j)
			}
		}
		if em.ContextInfo.Participant != nil {
			if j, err := types.ParseJID(*em.ContextInfo.Participant); err == nil {
				add(j)
			}
		}
	}
	for _, raw := range phoneMentionRE.FindAllString(text, -1) {
		j := types.NewJID(raw, types.DefaultUserServer)
		add(j)
	}
	return out
}
