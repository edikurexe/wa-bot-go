package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"wa-bot-go/internal/bot"
)

func main() {
	cfg := bot.LoadConfig()
	log.Println("🚀 Starting WA Bot Go...")
	log.Println("Session DB:", cfg.SessionDB)
	log.Println("Prefix:", cfg.Prefix)

	b, err := bot.New(cfg)
	if err != nil {
		log.Fatal(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := b.Run(ctx); err != nil {
		log.Fatal(err)
	}
}
