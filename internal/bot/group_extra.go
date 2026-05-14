package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/types"
)

type WarningEntry struct {
	Count       int       `json:"count"`
	BannedUntil time.Time `json:"banned_until,omitempty"`
}

func (b *Bot) warningsPath() string {
	return filepath.Join(filepath.Dir(b.cfg.SessionDB), "warnings.json")
}

func (b *Bot) loadWarnings() {
	data, err := os.ReadFile(b.warningsPath())
	if err != nil {
		return
	}
	var store map[string]map[string]*WarningEntry
	if err := json.Unmarshal(data, &store); err != nil {
		log.Println("warnings parse error:", err)
		return
	}
	b.warningMu.Lock()
	b.warnings = store
	b.warningMu.Unlock()
}

func (b *Bot) saveWarnings() {
	b.warningMu.Lock()
	data, err := json.MarshalIndent(b.warnings, "", "  ")
	b.warningMu.Unlock()
	if err != nil {
		log.Println("warnings marshal error:", err)
		return
	}
	if err := os.MkdirAll(filepath.Dir(b.warningsPath()), 0700); err != nil {
		log.Println("warnings mkdir error:", err)
		return
	}
	if err := os.WriteFile(b.warningsPath(), data, 0600); err != nil {
		log.Println("warnings write error:", err)
	}
}

func (b *Bot) isSenderAdmin(ctx context.Context, chat, sender types.JID) (bool, *types.GroupInfo, error) {
	info, err := b.client.GetGroupInfo(ctx, chat)
	if err != nil {
		return false, nil, err
	}
	for _, p := range info.Participants {
		if participantMatches(p, sender) && (p.IsAdmin || p.IsSuperAdmin) {
			return true, info, nil
		}
	}
	return false, info, nil
}

func participantMatches(p types.GroupParticipant, jid types.JID) bool {
	if jid.IsEmpty() {
		return false
	}
	if !p.JID.IsEmpty() && (p.JID == jid || p.JID.User == jid.User) {
		return true
	}
	if !p.PhoneNumber.IsEmpty() && (p.PhoneNumber == jid || p.PhoneNumber.User == jid.User) {
		return true
	}
	if !p.LID.IsEmpty() && (p.LID == jid || p.LID.User == jid.User) {
		return true
	}
	return false
}

func participantIsAdmin(p types.GroupParticipant) bool {
	return p.IsAdmin || p.IsSuperAdmin
}

func (b *Bot) requireGroupAdmin(ctx context.Context, chat, sender types.JID) (*types.GroupInfo, bool) {
	isAdmin, info, err := b.isSenderAdmin(ctx, chat, sender)
	if err != nil {
		b.sendText(ctx, chat, "❌ Gagal ambil data grup.")
		return nil, false
	}
	if !isAdmin {
		b.sendText(ctx, chat, "Command ini khusus admin grup.")
		return nil, false
	}
	return info, true
}

func (b *Bot) setAnnounceWithTimer(ctx context.Context, chat types.JID, text string, announce bool) {
	if err := b.client.SetGroupAnnounce(ctx, chat, announce); err != nil {
		b.sendText(ctx, chat, "❌ Gagal ubah setting grup. Pastikan bot admin.")
		return
	}

	action := "dibuka"
	revert := "ditutup"
	if announce {
		action = "ditutup"
		revert = "dibuka"
	}
	msg := "Grup " + action + " ✅"

	fields := strings.Fields(text)
	if len(fields) > 1 {
		if d, ok := parseGroupDuration(fields[1]); ok && d > 0 {
			msg += fmt.Sprintf("\nAkan otomatis %s lagi dalam %s.", revert, roundDuration(d))
			time.AfterFunc(d, func() {
				_ = b.client.SetGroupAnnounce(context.Background(), chat, !announce)
			})
		}
	}
	b.sendText(ctx, chat, msg)
}

func parseGroupDuration(raw string) (time.Duration, bool) {
	d, err := time.ParseDuration(raw)
	if err == nil {
		return d, true
	}
	var mins int
	if _, err := fmt.Sscanf(raw, "%d", &mins); err == nil && mins > 0 {
		return time.Duration(mins) * time.Minute, true
	}
	return 0, false
}

func (b *Bot) handleAbsen(ctx context.Context, chat, sender types.JID, text string, pushName string) {
	fields := strings.Fields(text)
	groupKey := chat.String()
	if len(fields) > 1 {
		switch strings.ToLower(fields[1]) {
		case "start", "mulai":
			b.absenStore.Store(groupKey, []string{})
			b.sendText(ctx, chat, "📌 *ABSEN DIMULAI*\nKetik .absen untuk ikut.\nKetik .absen cek untuk lihat daftar.\nKetik .absen reset untuk hapus.")
			return
		case "cek", "list":
			b.sendAbsenList(ctx, chat)
			return
		case "reset", "stop":
			b.absenStore.Delete(groupKey)
			b.sendText(ctx, chat, "✅ Sesi absen dihapus.")
			return
		}
	}

	val, ok := b.absenStore.Load(groupKey)
	if !ok {
		b.sendText(ctx, chat, "Belum ada sesi absen. Gunakan .absen start")
		return
	}
	list, _ := val.([]string)
	name := strings.TrimSpace(pushName)
	if name == "" {
		name = sender.User
	}
	entry := name + "|" + sender.String()
	for _, existing := range list {
		if strings.HasSuffix(existing, "|"+sender.String()) {
			b.sendText(ctx, chat, "Kamu sudah absen sebelumnya, "+name+".")
			return
		}
	}
	list = append(list, entry)
	b.absenStore.Store(groupKey, list)
	b.sendText(ctx, chat, fmt.Sprintf("✅ Berhasil absen: *%s*\nTotal: %d orang", name, len(list)))
}

func (b *Bot) sendAbsenList(ctx context.Context, chat types.JID) {
	val, ok := b.absenStore.Load(chat.String())
	if !ok {
		b.sendText(ctx, chat, "Tidak ada sesi absen aktif.")
		return
	}
	list, _ := val.([]string)
	if len(list) == 0 {
		b.sendText(ctx, chat, "Belum ada yang absen.")
		return
	}
	var body strings.Builder
	body.WriteString("📝 *DAFTAR ABSEN*\n\n")
	for i, item := range list {
		name := strings.SplitN(item, "|", 2)[0]
		body.WriteString(fmt.Sprintf("%d. %s\n", i+1, name))
	}
	b.sendText(ctx, chat, strings.TrimSpace(body.String()))
}

func (b *Bot) handleWarn(ctx context.Context, chat, sender types.JID, msg *proto.Message, text, label string) {
	info, ok := b.requireGroupAdmin(ctx, chat, sender)
	if !ok {
		return
	}
	targets := extractTargets(msg, text)
	if len(targets) == 0 {
		b.sendText(ctx, chat, "Reply/mention target. Contoh: ."+label+" @user")
		return
	}
	target := targets[0]
	if b.client.Store.ID != nil && target.User == b.client.Store.ID.User {
		b.sendText(ctx, chat, "Tidak bisa memberi warning ke bot sendiri.")
		return
	}
	for _, p := range info.Participants {
		if participantMatches(p, target) && participantIsAdmin(p) {
			b.sendText(ctx, chat, "Tidak bisa memberi warning ke owner/admin grup.")
			return
		}
	}

	groupKey := chat.String()
	userKey := target.ToNonAD().String()
	b.warningMu.Lock()
	if b.warnings[groupKey] == nil {
		b.warnings[groupKey] = map[string]*WarningEntry{}
	}
	if b.warnings[groupKey][userKey] == nil {
		b.warnings[groupKey][userKey] = &WarningEntry{}
	}
	b.warnings[groupKey][userKey].Count++
	count := b.warnings[groupKey][userKey].Count
	entry := b.warnings[groupKey][userKey]
	b.warningMu.Unlock()

	mention := target.ToNonAD().String()
	if count <= 3 {
		b.sendMentionText(ctx, chat, fmt.Sprintf("⚠️ *PERINGATAN %d/3*\n@%s mendapat warning ke-%d. Warning ke-4 akan dikick dan masuk catatan banned.", count, target.User, count), []string{mention})
		b.saveWarnings()
		return
	}

	banDuration := 24 * time.Hour
	banText := "24 jam"
	if count == 5 {
		banDuration = 48 * time.Hour
		banText = "48 jam"
	} else if count >= 6 {
		banDuration = 72 * time.Hour
		banText = "72 jam"
	}
	b.warningMu.Lock()
	entry.BannedUntil = time.Now().Add(banDuration)
	b.warningMu.Unlock()
	b.saveWarnings()

	b.sendMentionText(ctx, chat, fmt.Sprintf("🚫 *BANNED %s*\n@%s mendapat warning ke-%d dan akan dikick.", banText, target.User, count), []string{mention})
	if _, err := b.client.UpdateGroupParticipants(ctx, chat, []types.JID{target}, whatsmeow.ParticipantChangeRemove); err != nil {
		b.sendText(ctx, chat, "⚠️ Gagal kick member. Pastikan bot admin.")
	}
}

func (b *Bot) handleDeleteWarn(ctx context.Context, chat, sender types.JID, msg *proto.Message, text string, all bool) {
	if _, ok := b.requireGroupAdmin(ctx, chat, sender); !ok {
		return
	}
	targets := extractTargets(msg, text)
	if len(targets) == 0 {
		cmd := ".dw"
		if all {
			cmd = ".dwall"
		}
		b.sendText(ctx, chat, "Reply/mention target. Contoh: "+cmd+" @user")
		return
	}
	target := targets[0]
	groupKey := chat.String()
	userKey := target.ToNonAD().String()
	b.warningMu.Lock()
	current := 0
	if b.warnings[groupKey] != nil && b.warnings[groupKey][userKey] != nil {
		if all {
			delete(b.warnings[groupKey], userKey)
		} else if b.warnings[groupKey][userKey].Count > 0 {
			b.warnings[groupKey][userKey].Count--
			if b.warnings[groupKey][userKey].Count < 4 {
				b.warnings[groupKey][userKey].BannedUntil = time.Time{}
			}
			current = b.warnings[groupKey][userKey].Count
		}
	}
	b.warningMu.Unlock()
	b.saveWarnings()
	mention := target.ToNonAD().String()
	if all {
		b.sendMentionText(ctx, chat, fmt.Sprintf("✅ Semua warning @%s dihapus.", target.User), []string{mention})
	} else {
		b.sendMentionText(ctx, chat, fmt.Sprintf("✅ Warning @%s dikurangi. Sisa: *%d*", target.User, current), []string{mention})
	}
}

func (b *Bot) handleListWarn(ctx context.Context, chat types.JID) {
	b.warningMu.Lock()
	groupWarnings := b.warnings[chat.String()]
	items := make(map[string]WarningEntry, len(groupWarnings))
	for jid, entry := range groupWarnings {
		if entry != nil && entry.Count > 0 {
			items[jid] = *entry
		}
	}
	b.warningMu.Unlock()
	if len(items) == 0 {
		b.sendText(ctx, chat, "Tidak ada member yang punya warning di grup ini.")
		return
	}
	var body strings.Builder
	mentions := make([]string, 0, len(items))
	body.WriteString("📋 *DAFTAR WARNING*\n\n")
	i := 1
	for jid, entry := range items {
		parsed, _ := types.ParseJID(jid)
		mentions = append(mentions, jid)
		status := fmt.Sprintf("⚠️ %d warning", entry.Count)
		if entry.Count >= 4 {
			if time.Now().Before(entry.BannedUntil) {
				status = "🚫 banned, sisa " + roundDuration(time.Until(entry.BannedUntil))
			} else {
				status = fmt.Sprintf("🚫 warning %d, ban selesai", entry.Count)
			}
		}
		body.WriteString(fmt.Sprintf("%d. @%s - %s\n", i, parsed.User, status))
		i++
	}
	b.sendMentionText(ctx, chat, strings.TrimSpace(body.String()), mentions)
}
