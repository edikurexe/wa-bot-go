package bot

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"mime"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	qrterminal "github.com/mdp/qrterminal/v3"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	_ "modernc.org/sqlite"
)

type Bot struct {
	cfg       Config
	client    *whatsmeow.Client
	startedAt time.Time

	deletedMu sync.Mutex
	msgCache  map[string]cachedMessage
	msgOrder  []string

	seenMu   sync.Mutex
	seenMsgs map[string]time.Time

	settingsMu        sync.Mutex
	antiDeleteEnabled map[string]bool
	viewOnceEnabled   map[string]bool
	stickerMode       map[string]string

	absenStore sync.Map
	warningMu  sync.Mutex
	warnings   map[string]map[string]*WarningEntry

	stickerMu   sync.Mutex
	stickerSent map[string]time.Time
}

func New(cfg Config) (*Bot, error) {
	if err := os.MkdirAll(filepath.Dir(cfg.SessionDB), 0700); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(cfg.TempDir, 0700); err != nil {
		return nil, err
	}

	// Open once so modernc creates a valid DB file, then let sqlstore use it.
	db, err := sql.Open("sqlite", "file:"+cfg.SessionDB+"?_pragma=foreign_keys(1)")
	if err == nil {
		_ = db.Close()
	}

	logger := waLog.Stdout("WA", "INFO", true)
	container, err := sqlstore.New(context.Background(), "sqlite", "file:"+cfg.SessionDB+"?_pragma=foreign_keys(1)", logger)
	if err != nil {
		return nil, err
	}
	device, err := container.GetFirstDevice(context.Background())
	if err != nil {
		return nil, err
	}
	client := whatsmeow.NewClient(device, logger)
	b := &Bot{cfg: cfg, client: client, startedAt: time.Now(), msgCache: map[string]cachedMessage{}, seenMsgs: map[string]time.Time{}, antiDeleteEnabled: map[string]bool{}, viewOnceEnabled: map[string]bool{}, stickerMode: map[string]string{}, warnings: map[string]map[string]*WarningEntry{}, stickerSent: map[string]time.Time{}}
	b.loadSettings()
	b.loadWarnings()
	client.AddEventHandler(b.handleEvent)
	return b, nil
}

func (b *Bot) Run(ctx context.Context) error {
	if b.client.Store.ID == nil {
		qrChan, err := b.client.GetQRChannel(ctx)
		if err != nil {
			return err
		}
		if err := b.client.Connect(); err != nil {
			return err
		}
		for evt := range qrChan {
			switch evt.Event {
			case "code":
				_ = os.WriteFile(filepath.Join(filepath.Dir(b.cfg.SessionDB), "last-qr.txt"), []byte(evt.Code), 0600)
				fmt.Println("Scan QR ini dengan WhatsApp:")
				qrterminal.GenerateHalfBlock(evt.Code, qrterminal.L, os.Stdout)
			case "success":
				fmt.Println("Login sukses ✅")
			case "timeout":
				fmt.Println("QR timeout, restart bot untuk QR baru.")
			}
		}
	} else {
		if err := b.client.Connect(); err != nil {
			return err
		}
	}

	<-ctx.Done()
	b.client.Disconnect()
	return nil
}

func (b *Bot) handleEvent(evt any) {
	switch v := evt.(type) {
	case *events.Message:
		go b.handleMessage(context.Background(), v)
	case *events.UndecryptableMessage:
		if v.IsUnavailable && v.UnavailableType == events.UnavailableTypeViewOnce {
			log.Printf("👁️ view once unavailable requested chat=%s sender=%s id=%s", v.Info.Chat.String(), v.Info.Sender.String(), v.Info.ID)
		}
	case *events.Connected:
		log.Println("WA connected ✅")
	case *events.Disconnected:
		log.Println("WA disconnected")
	}
}

func (b *Bot) handleMessage(ctx context.Context, evt *events.Message) {
	if evt.Info.IsFromMe || evt.Info.Chat.Server == types.BroadcastServer {
		return
	}
	if b.seenMessage(evt) {
		return
	}
	msg := evt.Message
	if b.handleAntiDelete(ctx, evt) {
		return
	}
	if b.handleViewOnce(ctx, evt) {
		return
	}
	b.cacheMessage(evt)
	text := strings.TrimSpace(extractText(msg))
	lower := strings.ToLower(text)
	chat := evt.Info.Chat

	if text != "" {
		log.Printf("📩 [%s] %s", chat.String(), truncate(text, 120))
	}

	p := b.cfg.Prefix
	if b.handleGroupCommand(ctx, chat, evt.Info.Sender, msg, text, lower, evt.Info.PushName) {
		return
	}
	switch {
	case lower == p+"h" || lower == p+"help" || lower == p+"menu":
		b.sendText(ctx, chat, menuText(p))
	case lower == p+"ping":
		b.sendText(ctx, chat, "pong ✅")
	case lower == p+"owner":
		b.sendText(ctx, chat, "Owner: Mas Edi\nBot: WA Bot Go")
	case lower == p+"runtime" || lower == p+"uptime" || lower == p+"status":
		b.sendText(ctx, chat, b.statusText())
	case lower == p+"viewonce" || strings.HasPrefix(lower, p+"viewonce ") || lower == p+"once" || strings.HasPrefix(lower, p+"once "):
		b.handleViewOnceCommand(ctx, chat, text)
	case lower == p+"l" || strings.HasPrefix(lower, p+"l "):
		b.handleLookViewOnce(ctx, evt)
	case strings.HasPrefix(lower, p+"bratvideo"):
		payload := strings.TrimSpace(text[len(p+"bratvideo"):])
		if payload == "" {
			payload = quotedText(msg)
		}
		b.handleBratVideoSticker(ctx, evt, payload)
	case strings.HasPrefix(lower, p+"brat"):
		payload := strings.TrimSpace(text[len(p+"brat"):])
		if payload == "" {
			payload = quotedText(msg)
		}
		b.handleTextSticker(ctx, evt, payload, "brat")
	case lower == p+"ts" || strings.HasPrefix(lower, p+"ts "):
		payload := strings.TrimSpace(text[len(p+"ts"):])
		b.handleMemeSticker(ctx, evt, payload)
	case strings.HasPrefix(lower, p+"qc"):
		payload := strings.TrimSpace(text[len(p+"qc"):])
		if payload == "" {
			payload = quotedText(msg)
		}
		b.handleTextSticker(ctx, evt, payload, "quote")
	case strings.HasPrefix(lower, p+"tts"):
		b.sendText(ctx, chat, "Fitur TTS belum diaktifkan di clone Go. Nanti bisa disambung ke engine TTS/API.")
	case strings.HasPrefix(lower, p+"ai"):
		b.sendText(ctx, chat, "Fitur AI belum diaktifkan di clone Go. Nanti tinggal tambah API key/model.")
	case lower == p+"s" || lower == p+"sticker" || lower == p+"stiker":
		if b.reserveStickerSend(evt) {
			b.handleSticker(ctx, evt)
		}
	case lower == p+"stickermode" || strings.HasPrefix(lower, p+"stickermode ") || lower == p+"smode" || strings.HasPrefix(lower, p+"smode "):
		b.handleStickerModeCommand(ctx, chat, text)
	case lower == p+"toimg" || lower == p+"toimage" || lower == p+"ti":
		b.handleToImage(ctx, evt)
	case isDownloadCommandNoURL(lower, p):
		b.sendText(ctx, chat, downloadUsage(p, strings.TrimPrefix(lower, p)))
	case isDownloadCommandWithURL(lower, p):
		parts := strings.Fields(text)
		if len(parts) < 2 {
			b.sendText(ctx, chat, "Usage: "+p+"d <url>")
			return
		}
		b.react(ctx, evt, "⏳")
		if b.handleDownload(ctx, chat, restoreOriginalURL(parts[1])) {
			b.react(ctx, evt, "✅")
		} else {
			b.react(ctx, evt, "❌")
		}
	default:
		if u := firstURL(text); u != "" {
			b.react(ctx, evt, "⏳")
			if b.handleDownload(ctx, chat, restoreOriginalURL(u)) {
				b.react(ctx, evt, "✅")
			} else {
				b.react(ctx, evt, "❌")
			}
		}
	}
}

func (b *Bot) seenMessage(evt *events.Message) bool {
	if evt == nil || evt.Info.ID == "" {
		return false
	}
	key := evt.Info.Chat.String() + "|" + evt.Info.ID
	now := time.Now()
	b.seenMu.Lock()
	defer b.seenMu.Unlock()
	if _, ok := b.seenMsgs[key]; ok {
		return true
	}
	b.seenMsgs[key] = now
	for k, t := range b.seenMsgs {
		if now.Sub(t) > 10*time.Minute {
			delete(b.seenMsgs, k)
		}
	}
	return false
}

func (b *Bot) reserveStickerSend(evt *events.Message) bool {
	if evt == nil || evt.Info.ID == "" {
		return true
	}
	msgKey := stickerEventKey(evt, "cmd")
	cooldownKey := evt.Info.Chat.String() + "|" + evt.Info.Sender.String() + "|sticker-cooldown"
	now := time.Now()
	b.stickerMu.Lock()
	defer b.stickerMu.Unlock()
	if _, ok := b.stickerSent[msgKey]; ok {
		log.Println("skip duplicate sticker command same msg:", msgKey)
		return false
	}
	if last, ok := b.stickerSent[cooldownKey]; ok && now.Sub(last) < 8*time.Second {
		log.Println("skip duplicate sticker command cooldown:", cooldownKey)
		return false
	}
	b.stickerSent[msgKey] = now
	b.stickerSent[cooldownKey] = now
	for k, t := range b.stickerSent {
		if now.Sub(t) > 30*time.Minute {
			delete(b.stickerSent, k)
		}
	}
	return true
}

func (b *Bot) reserveStickerDelivery(evt *events.Message) bool {
	if evt == nil || evt.Info.ID == "" {
		return true
	}
	key := stickerEventKey(evt, "send")
	now := time.Now()
	b.stickerMu.Lock()
	defer b.stickerMu.Unlock()
	if _, ok := b.stickerSent[key]; ok {
		log.Println("skip duplicate sticker delivery same msg:", key)
		return false
	}
	b.stickerSent[key] = now
	for k, t := range b.stickerSent {
		if now.Sub(t) > 30*time.Minute {
			delete(b.stickerSent, k)
		}
	}
	return true
}

func stickerEventKey(evt *events.Message, scope string) string {
	return scope + "|" + evt.Info.Chat.String() + "|" + evt.Info.Sender.String() + "|" + evt.Info.ID
}

func menuText(p string) string {
	return "╭─〔 🤖 *WA BOT GO* 〕\n" +
		"│ Prefix: `" + p + "`\n" +
		"│ Status: Online ✅\n" +
		"╰───────────────\n\n" +
		"╭─〔 ⭐ *MAIN* 〕\n" +
		"│ " + p + "ping\n" +
		"│ " + p + "runtime\n" +
		"│ " + p + "owner\n" +
		"│ " + p + "l - reply foto/video sekali lihat\n" +
		"╰───────────────\n\n" +
		"╭─〔 🎬 *DOWNLOADER* 〕\n" +
		"│ " + p + "d <url>\n" +
		"│ " + p + "tt / " + p + "tiktok <link>\n" +
		"│ " + p + "ig <link>\n" +
		"│ " + p + "fb <link>\n" +
		"│ " + p + "yt <link>\n" +
		"│ auto-detect link aktif\n" +
		"╰───────────────\n\n" +
		"╭─〔 🖼️ *STICKER* 〕\n" +
		"│ " + p + "s\n" +
		"│ " + p + "smode original/crop\n" +
		"│ " + p + "toimg / " + p + "ti\n" +
		"│ " + p + "ts atas|bawah\n" +
		"│ " + p + "brat <teks>\n" +
		"│ " + p + "bratvideo <teks>\n" +
		"│ " + p + "qc <teks>\n" +
		"╰───────────────\n\n" +
		"╭─〔 🛡️ *GROUP* 〕\n" +
		"│ " + p + "group\n" +
		"│ " + p + "tagall <teks>\n" +
		"│ " + p + "hidetag <teks>\n" +
		"│ " + p + "open / " + p + "close\n" +
		"│ " + p + "oc / " + p + "cc [durasi]\n" +
		"│ " + p + "absen start/cek/reset\n" +
		"│ " + p + "w / " + p + "dw / " + p + "dwall\n" +
		"│ " + p + "listwarn\n" +
		"│ " + p + "kick @user\n" +
		"│ " + p + "promote / " + p + "demote\n" +
		"│ " + p + "antidelete on/off\n" +
		"╰───────────────\n\n" +
		"_Ketik_ *" + p + "group* _untuk detail fitur grup._"
}

func isDownloadCommandNoURL(lower, p string) bool {
	for _, cmd := range []string{"d", "dl", "download", "tt", "tiktok", "ig", "instagram", "fb", "facebook", "yt", "youtube", "x", "twitter"} {
		if lower == p+cmd {
			return true
		}
	}
	return false
}

func isDownloadCommandWithURL(lower, p string) bool {
	for _, cmd := range []string{"d", "dl", "download", "tt", "tiktok", "ig", "instagram", "fb", "facebook", "yt", "youtube", "x", "twitter"} {
		if strings.HasPrefix(lower, p+cmd+" ") {
			return true
		}
	}
	return false
}

func downloadUsage(p, cmd string) string {
	switch cmd {
	case "tt", "tiktok":
		return "Kirim link TikTok-nya. Contoh:\n" + p + "tt https://vt.tiktok.com/xxxx"
	case "ig", "instagram":
		return "Kirim link Instagram-nya. Contoh:\n" + p + "ig https://instagram.com/reel/xxxx"
	case "fb", "facebook":
		return "Kirim link Facebook-nya. Contoh:\n" + p + "fb https://facebook.com/reel/xxxx"
	case "yt", "youtube":
		return "Kirim link YouTube-nya. Contoh:\n" + p + "yt https://youtu.be/xxxx"
	default:
		return "Kirim link videonya. Contoh:\n" + p + "d https://vt.tiktok.com/xxxx"
	}
}

func extractText(m *proto.Message) string {
	if m == nil {
		return ""
	}
	if m.Conversation != nil {
		return *m.Conversation
	}
	if em := m.ExtendedTextMessage; em != nil && em.Text != nil {
		return *em.Text
	}
	if im := m.ImageMessage; im != nil && im.Caption != nil {
		return *im.Caption
	}
	if vm := m.VideoMessage; vm != nil && vm.Caption != nil {
		return *vm.Caption
	}
	return ""
}

func (b *Bot) sendText(ctx context.Context, chat types.JID, text string) {
	_, err := b.client.SendMessage(ctx, chat, &proto.Message{Conversation: &text})
	if err != nil {
		log.Println("send text error:", err)
	}
}

func (b *Bot) statusText() string {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return fmt.Sprintf("🤖 *WA Bot Go*\nStatus: online ✅\nUptime: %s\nRAM: %.1f MB\nGoroutine: %d", roundDuration(time.Since(b.startedAt)), float64(m.Alloc)/1024/1024, runtime.NumGoroutine())
}

func roundDuration(d time.Duration) string {
	if d < time.Minute {
		return d.Truncate(time.Second).String()
	}
	return d.Truncate(time.Minute).String()
}

func (b *Bot) react(ctx context.Context, evt *events.Message, emoji string) {
	_, _ = b.client.SendMessage(ctx, evt.Info.Chat, b.client.BuildReaction(evt.Info.Chat, evt.Info.Sender, evt.Info.ID, emoji))
}

func (b *Bot) handleSticker(ctx context.Context, evt *events.Message) {
	chat := evt.Info.Chat
	mediaMsg, kind := findMedia(evt.Message)
	if mediaMsg == nil {
		b.sendText(ctx, chat, "Kirim/reply gambar atau video + "+b.cfg.Prefix+"s")
		return
	}
	b.react(ctx, evt, "⏳")

	data, err := b.client.Download(ctx, mediaMsg)
	if err != nil {
		b.sendText(ctx, chat, "❌ Gagal download media.")
		return
	}

	mode := b.getStickerMode(chat)
	var webp []byte
	if kind == "video" {
		webp, err = makeVideoSticker(ctx, data, b.cfg, mode)
	} else {
		webp, err = makeImageSticker(ctx, data, b.cfg, mode)
	}
	if err != nil {
		log.Println("sticker error:", err)
		b.sendText(ctx, chat, "❌ Gagal membuat sticker.")
		return
	}
	if len(webp) > 1024*1024 {
		b.sendText(ctx, chat, "❌ Sticker terlalu besar. Coba video yang lebih pendek.")
		return
	}

	log.Printf("sending sticker for msg=%s chat=%s sender=%s", evt.Info.ID, chat.String(), evt.Info.Sender.String())
	if err := b.sendStickerWebPForEvent(ctx, evt, webp, kind == "video"); err != nil {
		log.Println("send sticker error:", err)
		b.sendText(ctx, chat, "❌ Gagal kirim sticker.")
		return
	}
	b.react(ctx, evt, "✅")
}

func (b *Bot) handleStickerModeCommand(ctx context.Context, chat types.JID, text string) {
	fields := strings.Fields(text)
	if len(fields) < 2 || strings.EqualFold(fields[1], "status") {
		mode := b.getStickerMode(chat)
		desc := "auto-crop kotak + rounded"
		if mode == "original" {
			desc = "original ratio/fit, tanpa crop"
		}
		b.sendText(ctx, chat, "Mode stiker: *"+mode+"*\n"+desc+"\n\nUbah:\n"+b.cfg.Prefix+"smode original\n"+b.cfg.Prefix+"smode crop")
		return
	}
	if !b.setStickerMode(chat, fields[1]) {
		b.sendText(ctx, chat, "Mode tidak dikenal. Pilih: original atau crop")
		return
	}
	mode := b.getStickerMode(chat)
	if mode == "original" {
		b.sendText(ctx, chat, "✅ Mode stiker: original ratio/fit, tanpa crop.")
	} else {
		b.sendText(ctx, chat, "✅ Mode stiker: auto-crop kotak + rounded.")
	}
}

func (b *Bot) handleMemeSticker(ctx context.Context, evt *events.Message, text string) {
	chat := evt.Info.Chat
	text = strings.TrimSpace(text)
	if text == "" {
		b.sendText(ctx, chat, "Reply gambar/stiker + "+b.cfg.Prefix+"ts teks\nPisah atas/bawah pakai | contoh: "+b.cfg.Prefix+"ts atas|bawah")
		return
	}
	parts := strings.SplitN(text, "|", 2)
	top := strings.TrimSpace(parts[0])
	bottom := ""
	if len(parts) == 2 {
		bottom = strings.TrimSpace(parts[1])
	}
	if top == "" && bottom == "" {
		b.sendText(ctx, chat, "Teks kosong.")
		return
	}
	if len([]rune(top))+len([]rune(bottom)) > 140 {
		b.sendText(ctx, chat, "Teks terlalu panjang, max ±140 karakter.")
		return
	}
	mediaMsg, kind := downloadableFromMessage(evt.Message)
	if mediaMsg == nil || (kind != "image" && kind != "sticker") {
		b.sendText(ctx, chat, "Reply gambar/stiker + "+b.cfg.Prefix+"ts teks")
		return
	}
	b.react(ctx, evt, "⏳")
	data, err := b.client.Download(ctx, mediaMsg)
	if err != nil {
		b.sendText(ctx, chat, "❌ Gagal download media.")
		b.react(ctx, evt, "❌")
		return
	}
	webp, err := makeMemeSticker(ctx, data, top, bottom, b.cfg)
	if err != nil {
		log.Println("meme sticker error:", err)
		b.sendText(ctx, chat, "❌ Gagal membuat meme sticker.")
		b.react(ctx, evt, "❌")
		return
	}
	if err := b.sendStickerWebPForEvent(ctx, evt, webp, false); err != nil {
		log.Println("send meme sticker error:", err)
		b.sendText(ctx, chat, "❌ Gagal kirim sticker.")
		b.react(ctx, evt, "❌")
		return
	}
	b.react(ctx, evt, "✅")
}

func (b *Bot) handleBratVideoSticker(ctx context.Context, evt *events.Message, text string) {
	chat := evt.Info.Chat
	text = strings.TrimSpace(text)
	if text == "" {
		b.sendText(ctx, chat, "Kirim teks. Contoh: "+b.cfg.Prefix+"bratvideo halo mas edi")
		return
	}
	if len([]rune(text)) > 80 {
		b.sendText(ctx, chat, "Teks terlalu panjang, max ±80 karakter.")
		return
	}
	b.react(ctx, evt, "⏳")
	webp, err := makeBratVideoSticker(ctx, text, b.cfg)
	if err != nil {
		log.Println("bratvideo sticker error:", err)
		b.sendText(ctx, chat, "❌ Gagal membuat bratvideo sticker.")
		b.react(ctx, evt, "❌")
		return
	}
	if len(webp) > 1024*1024 {
		b.sendText(ctx, chat, "❌ Sticker terlalu besar. Teksnya pendekin sedikit.")
		b.react(ctx, evt, "❌")
		return
	}
	if err := b.sendStickerWebPForEvent(ctx, evt, webp, true); err != nil {
		log.Println("send bratvideo sticker error:", err)
		b.sendText(ctx, chat, "❌ Gagal kirim sticker.")
		b.react(ctx, evt, "❌")
		return
	}
	b.react(ctx, evt, "✅")
}

func (b *Bot) handleTextSticker(ctx context.Context, evt *events.Message, text, style string) {
	chat := evt.Info.Chat
	text = strings.TrimSpace(text)
	if text == "" {
		if style == "quote" {
			b.sendText(ctx, chat, "Kirim/reply teks + "+b.cfg.Prefix+"qc")
		} else {
			b.sendText(ctx, chat, "Kirim teks. Contoh: "+b.cfg.Prefix+"brat halo mas edi")
		}
		return
	}
	if len([]rune(text)) > 180 {
		b.sendText(ctx, chat, "Teks terlalu panjang, max ±180 karakter.")
		return
	}
	b.react(ctx, evt, "⏳")
	webp, err := makeTextSticker(ctx, text, style, b.cfg)
	if err != nil {
		log.Println("text sticker error:", err)
		b.sendText(ctx, chat, "❌ Gagal membuat text sticker.")
		b.react(ctx, evt, "❌")
		return
	}
	if err := b.sendStickerWebPForEvent(ctx, evt, webp, false); err != nil {
		log.Println("send text sticker error:", err)
		b.sendText(ctx, chat, "❌ Gagal kirim sticker.")
		b.react(ctx, evt, "❌")
		return
	}
	b.react(ctx, evt, "✅")
}

func (b *Bot) handleToImage(ctx context.Context, evt *events.Message) {
	chat := evt.Info.Chat
	sticker := quotedSticker(evt.Message)
	if sticker == nil {
		b.sendText(ctx, chat, "Reply ke sticker + "+b.cfg.Prefix+"toimg")
		return
	}
	b.react(ctx, evt, "⏳")
	data, err := b.client.Download(ctx, sticker)
	if err != nil {
		b.sendText(ctx, chat, "❌ Gagal download sticker.")
		return
	}
	png, err := stickerToImage(ctx, data, b.cfg)
	if err != nil {
		log.Println("toimg error:", err)
		b.sendText(ctx, chat, "❌ Gagal convert sticker.")
		return
	}
	b.sendMedia(ctx, chat, png, "image/png", "")
	b.react(ctx, evt, "✅")
}

func (b *Bot) handleDownload(ctx context.Context, chat types.JID, rawURL string) bool {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	resolved := resolveURL(ctx, rawURL)

	if isTikTok(rawURL) || isTikTok(resolved) {
		if tiktokPhotoPattern.MatchString(resolved) || (tiktokShortPattern.MatchString(rawURL) && !strings.Contains(resolved, "/video/")) {
			if imgs, err := downloadTikTokPhotos(ctx, b.cfg, resolved); err == nil && len(imgs) > 0 {
				for i, img := range imgs {
					cap := ""
					if i == 0 {
						cap = fmt.Sprintf("📸 TikTok Slideshow (%d foto)", len(imgs))
					}
					b.sendMedia(ctx, chat, img, "image/jpeg", cap)
					time.Sleep(500 * time.Millisecond)
				}
				return true
			}
		}
	}

	vid, title, err := downloadVideo(ctx, b.cfg, rawURL)
	if err != nil && resolved != rawURL {
		vid, title, err = downloadVideo(ctx, b.cfg, resolved)
	}
	if err != nil {
		log.Println("download error:", err)
		b.sendText(ctx, chat, "❌ Gagal download. Video mungkin sudah dihapus atau URL tidak didukung.")
		return false
	}
	if title == "" {
		title = "Video"
	}
	b.sendMedia(ctx, chat, vid, "video/mp4", "🎬 "+title)
	return true
}

func (b *Bot) sendStickerWebP(ctx context.Context, chat types.JID, webp []byte, animated bool) error {
	uploaded, err := b.client.Upload(ctx, webp, whatsmeow.MediaImage)
	if err != nil {
		return err
	}
	msg := &proto.Message{StickerMessage: &proto.StickerMessage{
		URL:           &uploaded.URL,
		DirectPath:    &uploaded.DirectPath,
		MediaKey:      uploaded.MediaKey,
		FileEncSHA256: uploaded.FileEncSHA256,
		FileSHA256:    uploaded.FileSHA256,
		FileLength:    &uploaded.FileLength,
		Mimetype:      strPtr("image/webp"),
		IsAnimated:    boolPtr(animated),
	}}
	_, err = b.client.SendMessage(ctx, chat, msg)
	return err
}

func (b *Bot) sendStickerWebPForEvent(ctx context.Context, evt *events.Message, webp []byte, animated bool) error {
	if !b.reserveStickerDelivery(evt) {
		return nil
	}
	return b.sendStickerWebP(ctx, evt.Info.Chat, webp, animated)
}

func (b *Bot) sendMedia(ctx context.Context, chat types.JID, data []byte, mimetype, caption string) {
	mediaType := whatsmeow.MediaDocument
	if strings.HasPrefix(mimetype, "image/") {
		mediaType = whatsmeow.MediaImage
	} else if strings.HasPrefix(mimetype, "video/") {
		mediaType = whatsmeow.MediaVideo
	}
	u, err := b.client.Upload(ctx, data, mediaType)
	if err != nil {
		log.Println("upload media error:", err)
		b.sendText(ctx, chat, "❌ Gagal upload media.")
		return
	}
	if mimetype == "" {
		mimetype = httpDetect(data)
	}
	var msg *proto.Message
	if mediaType == whatsmeow.MediaImage {
		msg = &proto.Message{ImageMessage: &proto.ImageMessage{URL: &u.URL, DirectPath: &u.DirectPath, MediaKey: u.MediaKey, FileEncSHA256: u.FileEncSHA256, FileSHA256: u.FileSHA256, FileLength: &u.FileLength, Mimetype: &mimetype, Caption: &caption}}
	} else if mediaType == whatsmeow.MediaVideo {
		msg = &proto.Message{VideoMessage: &proto.VideoMessage{URL: &u.URL, DirectPath: &u.DirectPath, MediaKey: u.MediaKey, FileEncSHA256: u.FileEncSHA256, FileSHA256: u.FileSHA256, FileLength: &u.FileLength, Mimetype: &mimetype, Caption: &caption}}
	} else {
		fileName := "file" + extFromMime(mimetype)
		msg = &proto.Message{DocumentMessage: &proto.DocumentMessage{URL: &u.URL, DirectPath: &u.DirectPath, MediaKey: u.MediaKey, FileEncSHA256: u.FileEncSHA256, FileSHA256: u.FileSHA256, FileLength: &u.FileLength, Mimetype: &mimetype, FileName: &fileName, Caption: &caption}}
	}
	if _, err := b.client.SendMessage(ctx, chat, msg); err != nil {
		log.Println("send media error:", err)
	}
}

func findMedia(m *proto.Message) (whatsmeow.DownloadableMessage, string) {
	if m == nil {
		return nil, ""
	}
	if im := m.ImageMessage; im != nil {
		return im, "image"
	}
	if vm := m.VideoMessage; vm != nil {
		return vm, "video"
	}
	if em := m.ExtendedTextMessage; em != nil && em.ContextInfo != nil && em.ContextInfo.QuotedMessage != nil {
		return findMedia(em.ContextInfo.QuotedMessage)
	}
	return nil, ""
}

func quotedText(m *proto.Message) string {
	if m == nil || m.ExtendedTextMessage == nil || m.ExtendedTextMessage.ContextInfo == nil || m.ExtendedTextMessage.ContextInfo.QuotedMessage == nil {
		return ""
	}
	return extractText(m.ExtendedTextMessage.ContextInfo.QuotedMessage)
}

func quotedSticker(m *proto.Message) whatsmeow.DownloadableMessage {
	if m == nil || m.ExtendedTextMessage == nil || m.ExtendedTextMessage.ContextInfo == nil || m.ExtendedTextMessage.ContextInfo.QuotedMessage == nil {
		return nil
	}
	if st := m.ExtendedTextMessage.ContextInfo.QuotedMessage.StickerMessage; st != nil {
		return st
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
func strPtr(s string) *string       { return &s }
func boolPtr(b bool) *bool          { return &b }
func httpDetect(data []byte) string { return "application/octet-stream" }
func extFromMime(mt string) string {
	exts, _ := mime.ExtensionsByType(mt)
	if len(exts) > 0 {
		return exts[0]
	}
	return ".bin"
}
