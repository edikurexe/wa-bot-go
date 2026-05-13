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
	b := &Bot{cfg: cfg, client: client, startedAt: time.Now(), msgCache: map[string]cachedMessage{}, seenMsgs: map[string]time.Time{}, antiDeleteEnabled: map[string]bool{}, viewOnceEnabled: map[string]bool{}, stickerSent: map[string]time.Time{}}
	b.loadSettings()
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
	if b.handleGroupCommand(ctx, chat, evt.Info.Sender, msg, text, lower) {
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
	case strings.HasPrefix(lower, p+"brat"):
		payload := strings.TrimSpace(text[len(p+"brat"):])
		if payload == "" {
			payload = quotedText(msg)
		}
		b.handleTextSticker(ctx, evt, payload, "brat")
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
	case lower == p+"toimg" || lower == p+"toimage":
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
	msgKey := evt.Info.Chat.String() + "|" + evt.Info.Sender.String() + "|" + evt.Info.ID
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
		"│ " + p + "toimg\n" +
		"│ " + p + "brat <teks>\n" +
		"│ " + p + "qc <teks>\n" +
		"╰───────────────\n\n" +
		"╭─〔 🛡️ *GROUP* 〕\n" +
		"│ " + p + "group\n" +
		"│ " + p + "tagall <teks>\n" +
		"│ " + p + "hidetag <teks>\n" +
		"│ " + p + "open / " + p + "close\n" +
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

	var webp []byte
	if kind == "video" {
		webp, err = makeVideoSticker(ctx, data, b.cfg)
	} else {
		webp, err = makeImageSticker(ctx, data, b.cfg)
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
	if err := b.sendStickerWebP(ctx, chat, webp, kind == "video"); err != nil {
		log.Println("send sticker error:", err)
		b.sendText(ctx, chat, "❌ Gagal kirim sticker.")
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
	if err := b.sendStickerWebP(ctx, chat, webp, false); err != nil {
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
