# Install di Termux

Bot ini bisa jalan di Termux Android tanpa Chromium.

## One-line Install

```bash
pkg install -y curl
curl -L https://raw.githubusercontent.com/edikurexe/wa-bot-go/main/install-termux.sh | bash
```

Default install ke:

```txt
~/wa-bot-go
```

## Jalankan Bot

```bash
cd ~/wa-bot-go
./wa-bot-go
```

Pertama kali jalan, scan QR WhatsApp dari terminal Termux:

```txt
WhatsApp > Linked devices / Perangkat tertaut > Link a device
```

## Update Bot

```bash
cd ~/wa-bot-go
git pull
go build -o wa-bot-go ./cmd/bot
```

## Package yang Diinstall

- Go/Golang
- Git
- Python
- FFmpeg
- libwebp
- build tools: clang, make, pkg-config
- Python libs: yt-dlp, gallery-dl, pillow, requests

## Catatan

- Session WhatsApp disimpan lokal di `data/session.db`.
- Jangan share folder `data/` karena berisi session login.
- Downloader butuh internet stabil.
- Video besar akan dicoba dikompres otomatis agar bisa dikirim WhatsApp.

## Custom Folder

```bash
APP_DIR=$HOME/projects/wa-bot-go bash install-termux.sh
```

## Custom Repo

```bash
REPO_URL=https://github.com/username/repo.git bash install-termux.sh
```
