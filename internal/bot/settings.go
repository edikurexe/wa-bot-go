package bot

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"

	"go.mau.fi/whatsmeow/types"
)

type settingsFile struct {
	AntiDelete  map[string]bool   `json:"antiDelete"`
	ViewOnce    map[string]bool   `json:"viewOnce"`
	StickerMode map[string]string `json:"stickerMode"`
}

func (b *Bot) settingsPath() string {
	return filepath.Join(filepath.Dir(b.cfg.SessionDB), "settings.json")
}

func (b *Bot) loadSettings() {
	path := b.settingsPath()
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var sf settingsFile
	if err := json.Unmarshal(data, &sf); err != nil {
		log.Println("settings parse error:", err)
		return
	}
	b.settingsMu.Lock()
	defer b.settingsMu.Unlock()
	if sf.AntiDelete != nil {
		b.antiDeleteEnabled = sf.AntiDelete
	}
	if sf.ViewOnce != nil {
		b.viewOnceEnabled = sf.ViewOnce
	}
	if sf.StickerMode != nil {
		b.stickerMode = sf.StickerMode
	}
}

func (b *Bot) saveSettings() {
	b.settingsMu.Lock()
	sf := settingsFile{AntiDelete: b.antiDeleteEnabled, ViewOnce: b.viewOnceEnabled, StickerMode: b.stickerMode}
	b.settingsMu.Unlock()

	data, err := json.MarshalIndent(sf, "", "  ")
	if err != nil {
		log.Println("settings marshal error:", err)
		return
	}
	if err := os.MkdirAll(filepath.Dir(b.settingsPath()), 0700); err != nil {
		log.Println("settings mkdir error:", err)
		return
	}
	if err := os.WriteFile(b.settingsPath(), data, 0600); err != nil {
		log.Println("settings write error:", err)
	}
}

func (b *Bot) isAntiDeleteEnabled(chat types.JID) bool {
	b.settingsMu.Lock()
	defer b.settingsMu.Unlock()
	return b.antiDeleteEnabled[chat.String()]
}

func (b *Bot) setAntiDelete(chat types.JID, enabled bool) {
	b.settingsMu.Lock()
	b.antiDeleteEnabled[chat.String()] = enabled
	b.settingsMu.Unlock()
	b.saveSettings()
}

func (b *Bot) isViewOnceEnabled(chat types.JID) bool {
	b.settingsMu.Lock()
	defer b.settingsMu.Unlock()
	return b.viewOnceEnabled[chat.String()]
}

func (b *Bot) setViewOnce(chat types.JID, enabled bool) {
	b.settingsMu.Lock()
	b.viewOnceEnabled[chat.String()] = enabled
	b.settingsMu.Unlock()
	b.saveSettings()
}

func (b *Bot) getStickerMode(chat types.JID) string {
	b.settingsMu.Lock()
	defer b.settingsMu.Unlock()
	mode := b.stickerMode[chat.String()]
	if mode != "original" && mode != "crop" {
		return "crop"
	}
	return mode
}

func (b *Bot) setStickerMode(chat types.JID, mode string) bool {
	mode = strings.ToLower(strings.TrimSpace(mode))
	switch mode {
	case "crop", "rounded", "bulat", "auto", "autocrop":
		mode = "crop"
	case "original", "ori", "normal", "fit":
		mode = "original"
	default:
		return false
	}
	b.settingsMu.Lock()
	b.stickerMode[chat.String()] = mode
	b.settingsMu.Unlock()
	b.saveSettings()
	return true
}
