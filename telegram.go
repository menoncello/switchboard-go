package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"
)

type TelegramConfig struct {
	BotToken string
	ChatID   string
}

// telegramAPIBase is a var so tests can point it at a fake server.
var telegramAPIBase = "https://api.telegram.org"

// TelegramNotifier warns once per failure episode: it disarms on the first
// failure and re-arms when any tier serves a request.
type TelegramNotifier struct {
	cfg      TelegramConfig
	mu       sync.Mutex
	disarmed bool
}

func NewTelegramNotifier(cfg TelegramConfig) *TelegramNotifier { return &TelegramNotifier{cfg: cfg} }

func (n *TelegramNotifier) configured() bool { return n.cfg.BotToken != "" && n.cfg.ChatID != "" }

func (n *TelegramNotifier) notifyFailure(model string) {
	if !n.configured() {
		return
	}
	n.mu.Lock()
	first := !n.disarmed
	n.disarmed = true
	n.mu.Unlock()
	if first {
		go n.send("Switchboard Go: no tier could serve a request (model: " + truncateModel(model) + ")")
	}
}

func (n *TelegramNotifier) rearm() {
	n.mu.Lock()
	n.disarmed = false
	n.mu.Unlock()
}

func (n *TelegramNotifier) send(text string) {
	body, _ := json.Marshal(map[string]string{"chat_id": n.cfg.ChatID, "text": text})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, telegramAPIBase+"/bot"+n.cfg.BotToken+"/sendMessage", bytes.NewReader(body))
	if err != nil {
		log.Printf("telegram notification failed: bad request")
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		// the *url.Error carries the URL with the token, so the raw error is never logged
		log.Printf("telegram notification failed: request error")
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		log.Printf("telegram notification failed: status %d", resp.StatusCode)
	}
}
