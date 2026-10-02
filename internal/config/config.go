package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"time"
	_ "time/tzdata"
)

// Config содержит всю конфигурацию приложения
type Config struct {
	TelegramBotToken   string
	OwnerTelegramID    int64
	DatabasePath       string
	GroupTimezone      string
	LogLevel           string
	WebhookURL         string
	WebhookPathSecret  string
	WebhookSecretToken string
	HTTPListenAddr     string
}

// Load загружает конфигурацию из переменных окружения
func Load() (*Config, error) {
	cfg := &Config{
		GroupTimezone:  "Europe/Minsk",
		LogLevel:       "info",
		HTTPListenAddr: ":8080",
	}

	cfg.TelegramBotToken = os.Getenv("TELEGRAM_BOT_TOKEN")
	if cfg.TelegramBotToken == "" {
		return nil, fmt.Errorf("TELEGRAM_BOT_TOKEN is required")
	}

	ownerIDStr := os.Getenv("OWNER_TELEGRAM_ID")
	if ownerIDStr == "" {
		return nil, fmt.Errorf("OWNER_TELEGRAM_ID is required")
	}
	ownerID, err := strconv.ParseInt(ownerIDStr, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("OWNER_TELEGRAM_ID must be a valid integer: %w", err)
	}
	cfg.OwnerTelegramID = ownerID

	cfg.DatabasePath = os.Getenv("DATABASE_PATH")
	if cfg.DatabasePath == "" {
		return nil, fmt.Errorf("DATABASE_PATH is required")
	}

	cfg.GroupTimezone = os.Getenv("GROUP_TIMEZONE")
	if cfg.GroupTimezone == "" {
		cfg.GroupTimezone = "Europe/Minsk"
	}

	cfg.LogLevel = os.Getenv("LOG_LEVEL")
	if cfg.LogLevel == "" {
		cfg.LogLevel = "info"
	}

	cfg.WebhookURL = os.Getenv("WEBHOOK_URL")
	if cfg.WebhookURL == "" {
		return nil, fmt.Errorf("WEBHOOK_URL is required for webhook mode")
	}

	cfg.WebhookPathSecret = os.Getenv("WEBHOOK_PATH_SECRET")
	if cfg.WebhookPathSecret == "" {
		return nil, fmt.Errorf("WEBHOOK_PATH_SECRET is required")
	}

	cfg.WebhookSecretToken = os.Getenv("WEBHOOK_SECRET_TOKEN")
	if cfg.WebhookSecretToken == "" {
		return nil, fmt.Errorf("WEBHOOK_SECRET_TOKEN is required")
	}

	cfg.HTTPListenAddr = os.Getenv("HTTP_LISTEN_ADDR")
	if cfg.HTTPListenAddr == "" {
		cfg.HTTPListenAddr = ":8080"
	}

	if cfg.OwnerTelegramID <= 0 {
		return nil, fmt.Errorf("OWNER_TELEGRAM_ID must be positive")
	}
	u, err := url.Parse(cfg.WebhookURL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, fmt.Errorf("WEBHOOK_URL must be a public HTTPS base URL without path, credentials, query or fragment")
	}
	valid := regexp.MustCompile(`^[A-Za-z0-9_-]{1,256}$`)
	if !valid.MatchString(cfg.WebhookPathSecret) || !valid.MatchString(cfg.WebhookSecretToken) {
		return nil, fmt.Errorf("webhook secrets must contain 1–256 letters, digits, underscores or hyphens")
	}
	if _, err := time.LoadLocation(cfg.GroupTimezone); err != nil {
		return nil, fmt.Errorf("GROUP_TIMEZONE is invalid")
	}
	if _, _, err := net.SplitHostPort(cfg.HTTPListenAddr); err != nil {
		return nil, fmt.Errorf("HTTP_LISTEN_ADDR is invalid")
	}
	switch cfg.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return nil, fmt.Errorf("LOG_LEVEL must be debug, info, warn or error")
	}
	return cfg, nil
}
