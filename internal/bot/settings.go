package bot

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"

	"go.mau.fi/whatsmeow/types"
)

type settingsFile struct {
	AntiDelete map[string]bool `json:"antiDelete"`
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
}

func (b *Bot) saveSettings() {
	b.settingsMu.Lock()
	sf := settingsFile{AntiDelete: b.antiDeleteEnabled}
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
