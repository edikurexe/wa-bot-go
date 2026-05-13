#!/data/data/com.termux/files/usr/bin/bash

set -euo pipefail

GREEN="\033[0;32m"
YELLOW="\033[1;33m"
RED="\033[0;31m"
NC="\033[0m"

log() { echo -e "${GREEN}[OK]${NC} $1"; }
info() { echo -e "${YELLOW}[INFO]${NC} $1"; }
fail() { echo -e "${RED}[ERROR]${NC} $1"; exit 1; }

if [ -z "${PREFIX:-}" ] || [[ "$PREFIX" != *"com.termux"* ]]; then
  fail "Script ini khusus Termux. Jalankan dari aplikasi Termux."
fi

REPO_URL="${REPO_URL:-https://github.com/edikurexe/wa-bot-go.git}"
APP_DIR="${APP_DIR:-$HOME/wa-bot-go}"
BIN_NAME="wa-bot-go"

info "Update package Termux..."
pkg update -y
pkg upgrade -y

info "Install dependency bot..."
pkg install -y \
  golang \
  git \
  python \
  ffmpeg \
  libwebp \
  clang \
  make \
  pkg-config \
  openssl \
  ca-certificates \
  curl \
  wget \
  nano

info "Install dependency Python..."
python -m pip install --upgrade pip wheel setuptools
python -m pip install --upgrade yt-dlp gallery-dl pillow requests

info "Setup storage Android jika belum..."
if [ ! -d "$HOME/storage" ]; then
  termux-setup-storage || info "Storage permission dilewati. Izinkan manual kalau popup muncul."
fi

if [ -d "$APP_DIR/.git" ]; then
  info "Repo sudah ada, update..."
  cd "$APP_DIR"
  git pull --ff-only || info "Git pull gagal, lanjut pakai source lokal."
else
  info "Clone repo ke $APP_DIR..."
  rm -rf "$APP_DIR"
  git clone "$REPO_URL" "$APP_DIR"
  cd "$APP_DIR"
fi

info "Build bot..."
go mod tidy
go build -o "$BIN_NAME" ./cmd/bot
chmod +x "$BIN_NAME"

log "Install selesai."
echo ""
echo "Cara jalanin bot:"
echo "  cd $APP_DIR"
echo "  ./$BIN_NAME"
echo ""
echo "Pertama kali jalan, scan QR WhatsApp dari terminal Termux."
echo ""
echo "Optional env:"
echo "  WA_PREFIX=. ./$BIN_NAME"
echo ""
echo "Kalau mau update nanti:"
echo "  cd $APP_DIR && git pull && go build -o $BIN_NAME ./cmd/bot"
