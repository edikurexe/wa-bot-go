# WA Bot Go

Clone awal dari `/home/edi/wa-bot/bot.js`, tapi pakai Go + `whatsmeow` supaya tidak butuh Chromium/Puppeteer.

## Status

✅ Sudah build sukses.

Fitur yang sudah di-port:

- `.menu` / `.help` / `.h`
- `.s` / `.sticker` / `.stiker` — gambar/video jadi sticker WebP
- `.toimg` / `.toimage` — sticker jadi PNG
- `.d <url>` / `.dl <url>` / `.download <url>` — downloader via helper Python lama
- Auto-detect link YouTube, TikTok, Instagram, Facebook, X/Twitter
- TikTok slideshow/photo via `scripts/tiktok-photo.py`
- Session SQLite terpisah: `data/session.db`

## Catatan Migrasi

Session dari `whatsapp-web.js` tidak bisa dipakai langsung di `whatsmeow`, jadi perlu scan QR ulang.

Bot lama tidak disentuh. Folder lama tetap:

```txt
/home/edi/wa-bot
```

Clone Go ini ada di:

```txt
/home/edi/wa-bot-go
```

## Dependency Sistem

Pastikan ada:

```bash
ffmpeg
python3
yt-dlp dependency yang dipakai ytdl.py lama
```

Go sudah terinstall user-level di server ini:

```bash
export PATH="$HOME/.local/go/bin:$PATH"
```

## Build

```bash
cd /home/edi/wa-bot-go
export PATH="$HOME/.local/go/bin:$PATH"
go build -o wa-bot-go ./cmd/bot
```

## Run Manual Test

```bash
cd /home/edi/wa-bot-go
export PATH="$HOME/.local/go/bin:$PATH"
./wa-bot-go
```

Saat pertama jalan, scan QR yang muncul di terminal.

## Environment Optional

```bash
WA_PREFIX=.
WA_SESSION_DB=/home/edi/wa-bot-go/data/session.db
WA_TEMP_DIR=/home/edi/wa-bot-go/tmp
WA_SCRIPTS_DIR=/home/edi/wa-bot-go/scripts
```

## Switch Aman dari Bot Lama

1. Jalankan Go bot manual dulu.
2. Scan QR dan test `.menu`, `.s`, `.d <url>`.
3. Kalau sudah stabil, baru stop service/process bot lama Chromium.
4. Baru buat systemd service untuk `wa-bot-go`.

Jangan jalankan bot lama dan bot Go dengan nomor WhatsApp yang sama terlalu lama bersamaan, karena multi-device/session bisa bentrok atau logout.
