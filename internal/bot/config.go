package bot

import (
	"os"
	"path/filepath"
)

type Config struct {
	Prefix             string
	SessionDB          string
	TempDir            string
	ScriptsDir         string
	MaxVideoSize       int64
	MaxStickerVideoSec int
}

func LoadConfig() Config {
	wd, _ := os.Getwd()
	prefix := getenv("WA_PREFIX", ".")
	return Config{
		Prefix:             prefix,
		SessionDB:          getenv("WA_SESSION_DB", filepath.Join(wd, "data", "session.db")),
		TempDir:            getenv("WA_TEMP_DIR", filepath.Join(wd, "tmp")),
		ScriptsDir:         getenv("WA_SCRIPTS_DIR", filepath.Join(wd, "scripts")),
		MaxVideoSize:       16 * 1024 * 1024,
		MaxStickerVideoSec: 8,
	}
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
