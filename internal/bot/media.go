package bot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var (
	videoPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)(?:https?://)?(?:www\.|m\.)?(?:youtube\.com/(?:watch\?v=|shorts/|live/)|youtu\.be/)[\w-]+`),
		regexp.MustCompile(`(?i)(?:https?://)?(?:(?:www|vm|vt|m)\.)?tiktok\.com/[^\s]+`),
		// Instagram has several share URL variants now, e.g.
		// /reel/<code>/, /p/<code>/, /stories/<user>/<id>/, and /share/reel/<token>/.
		// Capture the full non-space URL so query params/redirect tokens are not lost.
		regexp.MustCompile(`(?i)(?:https?://)?(?:www\.)?instagram\.com/(?:reel|reels|p|tv|stories|share/(?:reel|p|video))/[^\s]+`),
		regexp.MustCompile(`(?i)(?:https?://)?(?:www\.|m\.|web\.)?facebook\.com/(?:share/(?:v|r)/|watch/|reel/|[\w.]+/videos/)[^\s]+`),
		regexp.MustCompile(`(?i)(?:https?://)?fb\.watch/[^\s]+`),
		regexp.MustCompile(`(?i)(?:https?://)?(?:www\.)?(?:twitter|x)\.com/\w+/status/\d+`),
		regexp.MustCompile(`(?i)(?:https?://)?t\.co/[\w]+`),
	}
	tiktokPhotoPattern = regexp.MustCompile(`(?i)tiktok\.com/@[\w.-]+/photo/\d+`)
	tiktokShortPattern = regexp.MustCompile(`(?i)(?:vm|vt)\.tiktok\.com/[\w]+`)
	tiktokAnyPattern   = regexp.MustCompile(`(?i)tiktok\.com`)
)

func firstURL(text string) string {
	for _, p := range videoPatterns {
		if m := p.FindString(text); m != "" {
			return strings.TrimRight(m, ".,;)\"]}")
		}
	}
	return ""
}

func isTikTok(s string) bool { return tiktokAnyPattern.MatchString(s) }

func resolveURL(ctx context.Context, raw string) string {
	if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
		raw = "https://" + raw
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return raw
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return raw
	}
	defer resp.Body.Close()
	if resp.Request != nil && resp.Request.URL != nil {
		return resp.Request.URL.String()
	}
	return raw
}

func restoreOriginalURL(matched string) string {
	if !strings.Contains(matched, "/404?fromUrl=") {
		return matched
	}
	u, err := url.Parse(matched)
	if err != nil {
		return matched
	}
	from := u.Query().Get("fromUrl")
	if from == "" {
		return matched
	}
	return "https://vm.tiktok.com" + from
}

func makeImageSticker(ctx context.Context, input []byte, cfg Config) ([]byte, error) {
	in, out := tempPair(cfg.TempDir, "img", ".bin", ".webp")
	defer cleanup(in, out)
	if err := os.WriteFile(in, input, 0600); err != nil {
		return nil, err
	}
	// Match old Node bot behavior: center square crop -> 512x512 -> rounded corners -> WebP.
	// Implemented via Pillow helper because ffmpeg rounded-alpha filters are brittle across builds.
	cmd := exec.CommandContext(ctx, "python3", filepath.Join(cfg.ScriptsDir, "image-sticker.py"), in, out)
	if b, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("image sticker helper: %w: %s", err, string(b))
	}
	return os.ReadFile(out)
}

func makeVideoSticker(ctx context.Context, input []byte, cfg Config) ([]byte, error) {
	in, out := tempPair(cfg.TempDir, "vid", ".mp4", ".webp")
	defer cleanup(in, out)
	if err := os.WriteFile(in, input, 0600); err != nil {
		return nil, err
	}
	vf := "crop='min(iw,ih)':'min(iw,ih)',scale=512:512,fps=12"
	cmd := exec.CommandContext(ctx, "ffmpeg", "-y", "-i", in, "-t", fmt.Sprint(cfg.MaxStickerVideoSec), "-vf", vf, "-c:v", "libwebp_anim", "-loop", "0", "-q:v", "80", "-compression_level", "0", "-an", out)
	if b, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("ffmpeg video sticker: %w: %s", err, string(b))
	}
	return os.ReadFile(out)
}

func makeTextSticker(ctx context.Context, text, style string, cfg Config) ([]byte, error) {
	in, out := tempPair(cfg.TempDir, "text", ".svg", ".webp")
	defer cleanup(in, out)

	bg := "#ffffff"
	fg := "#111111"
	fontSize := 46
	weight := "700"
	if style == "quote" {
		bg = "#101010"
		fg = "#ffffff"
		fontSize = 34
		weight = "600"
	}
	lines := wrapRunes(text, 18)
	lineHeight := fontSize + 12
	totalHeight := len(lines) * lineHeight
	startY := 256 - totalHeight/2 + fontSize

	var body strings.Builder
	for i, line := range lines {
		y := startY + i*lineHeight
		body.WriteString(fmt.Sprintf(`<text x="256" y="%d" text-anchor="middle" font-family="Arial, Helvetica, sans-serif" font-size="%d" font-weight="%s" fill="%s">%s</text>`, y, fontSize, weight, fg, html.EscapeString(line)))
	}
	if style == "quote" {
		body.WriteString(`<text x="256" y="455" text-anchor="middle" font-family="Arial, Helvetica, sans-serif" font-size="22" fill="#bdbdbd">WA Bot Go</text>`)
	}

	svg := fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="512" height="512"><rect width="512" height="512" rx="44" fill="%s"/>%s</svg>`, bg, body.String())
	if err := os.WriteFile(in, []byte(svg), 0600); err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, "ffmpeg", "-y", "-i", in, "-vcodec", "libwebp", "-lossless", "0", "-q:v", "85", out)
	if b, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("ffmpeg text sticker: %w: %s", err, string(b))
	}
	return os.ReadFile(out)
}

func wrapRunes(s string, max int) []string {
	words := strings.Fields(s)
	if len(words) == 0 {
		return []string{s}
	}
	var lines []string
	var cur string
	for _, w := range words {
		if len([]rune(cur))+len([]rune(w))+1 > max && cur != "" {
			lines = append(lines, cur)
			cur = w
		} else if cur == "" {
			cur = w
		} else {
			cur += " " + w
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	if len(lines) > 8 {
		lines = lines[:8]
	}
	return lines
}

func stickerToImage(ctx context.Context, input []byte, cfg Config) ([]byte, error) {
	in, out := tempPair(cfg.TempDir, "sticker", ".webp", ".png")
	defer cleanup(in, out)
	if err := os.WriteFile(in, input, 0600); err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, "ffmpeg", "-y", "-i", in, out)
	if b, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("ffmpeg sticker to image: %w: %s", err, string(b))
	}
	return os.ReadFile(out)
}

type dlResult struct {
	OK     bool     `json:"ok"`
	Error  string   `json:"error"`
	Title  string   `json:"title"`
	Images []string `json:"images"`
}

func downloadVideo(ctx context.Context, cfg Config, rawURL string) ([]byte, string, error) {
	out := filepath.Join(cfg.TempDir, fmt.Sprintf("vid_%d.mp4", time.Now().UnixNano()))
	defer cleanup(out)
	cmd := exec.CommandContext(ctx, "python3", filepath.Join(cfg.ScriptsDir, "ytdl.py"), "download", rawURL, out)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		return nil, "", err
	}
	res := parseDL(stdout.Bytes())
	if !res.OK {
		return nil, "", errors.New(res.Error)
	}
	info, err := os.Stat(out)
	if err != nil {
		return nil, "", err
	}
	if info.Size() > cfg.MaxVideoSize {
		return nil, "", fmt.Errorf("video terlalu besar, max %d MB", cfg.MaxVideoSize/1024/1024)
	}
	b, err := os.ReadFile(out)
	return b, res.Title, err
}

func downloadTikTokPhotos(ctx context.Context, cfg Config, rawURL string) ([][]byte, error) {
	outDir := filepath.Join(cfg.TempDir, fmt.Sprintf("photos_%d", time.Now().UnixNano()))
	defer os.RemoveAll(outDir)
	cmd := exec.CommandContext(ctx, "python3", filepath.Join(cfg.ScriptsDir, "tiktok-photo.py"), "download", rawURL, outDir)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	res := parseDL(stdout.Bytes())
	if !res.OK || len(res.Images) == 0 {
		return nil, errors.New("photo download gagal")
	}
	var images [][]byte
	for i, p := range res.Images {
		if i >= 10 {
			break
		}
		info, err := os.Stat(p)
		if err != nil || info.Size() > 5*1024*1024 {
			continue
		}
		b, err := os.ReadFile(p)
		if err == nil {
			images = append(images, b)
		}
	}
	if len(images) == 0 {
		return nil, fs.ErrNotExist
	}
	return images, nil
}

func parseDL(b []byte) dlResult {
	var r dlResult
	if err := json.Unmarshal(bytes.TrimSpace(b), &r); err != nil {
		r.OK = false
		r.Error = strings.TrimSpace(string(b))
	}
	return r
}

func tempPair(dir, prefix, inExt, outExt string) (string, string) {
	_ = os.MkdirAll(dir, 0700)
	ts := time.Now().UnixNano()
	return filepath.Join(dir, fmt.Sprintf("%s_%d%s", prefix, ts, inExt)), filepath.Join(dir, fmt.Sprintf("%s_%d%s", prefix, ts, outExt))
}

func cleanup(paths ...string) {
	for _, p := range paths {
		_ = os.Remove(p)
	}
}
