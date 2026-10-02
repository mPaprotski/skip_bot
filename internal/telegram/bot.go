package telegram

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/poseshaemost/skip-bot/internal/config"
	"github.com/poseshaemost/skip-bot/internal/service"
	tele "gopkg.in/telebot.v3"
)

type Bot struct {
	api      *tele.Bot
	services *service.Services
	config   *config.Config
	logger   *slog.Logger
	ctx      context.Context
	failed   bool
}
type contextTransport struct {
	ctx  context.Context
	base http.RoundTripper
}

func (t contextTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	ctx, cancel := context.WithCancel(r.Context())
	stop := context.AfterFunc(t.ctx, cancel)
	response, e := t.base.RoundTrip(r.Clone(ctx))
	if e != nil {
		stop()
		cancel()
		return nil, e
	}
	response.Body = &contextBody{ReadCloser: response.Body, cancel: cancel, stop: stop}
	return response, nil
}

type contextBody struct {
	io.ReadCloser
	cancel context.CancelFunc
	stop   func() bool
}

func (b *contextBody) Close() error { defer b.cancel(); defer b.stop(); return b.ReadCloser.Close() }

func NewBot(ctx context.Context, cfg *config.Config, s *service.Services, logger *slog.Logger) (*Bot, error) {
	b := &Bot{services: s, config: cfg, logger: logger, ctx: ctx}
	client := &http.Client{Timeout: 10 * time.Second, Transport: contextTransport{ctx, http.DefaultTransport}}
	api, e := tele.NewBot(tele.Settings{Token: cfg.TelegramBotToken, Client: client, Synchronous: true, OnError: func(e error, c tele.Context) { b.failed = true; logger.Error("Telegram delivery or handler failed") }})
	if e != nil {
		return nil, errors.New("не удалось подключиться к Telegram API; проверьте токен и соединение")
	}
	b.api = api
	for _, command := range []string{"/start", "/help", "/absent", "/mystats", "/settings", "/admin", "/group", "/cancel"} {
		api.Handle(command, b.handle)
	}
	api.Handle(tele.OnText, b.handle)
	api.Handle(tele.OnCallback, b.handle)
	return b, nil
}
func (b *Bot) RegisterWebhook() error {
	current, e := b.api.Webhook()
	if e != nil {
		return errors.New("getWebhookInfo: Telegram API недоступен")
	}
	url := strings.TrimRight(b.config.WebhookURL, "/") + "/webhook/" + b.config.WebhookPathSecret
	// Telegram does not expose the current secret token. Reapply on restart even
	// when the URL is unchanged so secret rotation takes effect reliably.
	if current.Listen != url {
		b.logger.Info("registering webhook")
	} else {
		b.logger.Info("refreshing webhook authentication")
	}
	e = b.api.SetWebhook(&tele.Webhook{Endpoint: &tele.WebhookEndpoint{PublicURL: url}, SecretToken: b.config.WebhookSecretToken, MaxConnections: 1, AllowedUpdates: []string{"message", "callback_query"}})
	if e != nil {
		return errors.New("setWebhook: не удалось зарегистрировать публичный HTTPS webhook")
	}
	return nil
}
func (b *Bot) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), time.Second)
		defer cancel()
		if e := b.services.Storage.DB.PingContext(ctx); e != nil {
			http.Error(w, "unavailable", 503)
			return
		}
		w.WriteHeader(200)
	})
	mux.HandleFunc("/webhook/"+b.config.WebhookPathSecret, b.webhook)
	return mux
}
func (b *Bot) webhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", 405)
		return
	}
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Telegram-Bot-Api-Secret-Token")), []byte(b.config.WebhookSecretToken)) != 1 {
		http.Error(w, "forbidden", 403)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	defer r.Body.Close()
	raw, e := io.ReadAll(r.Body)
	if e != nil {
		var size *http.MaxBytesError
		if errors.As(e, &size) {
			http.Error(w, "too large", 413)
		} else {
			http.Error(w, "bad request", 400)
		}
		return
	}
	var u tele.Update
	if e = json.Unmarshal(raw, &u); e != nil || u.ID < 0 || (u.Message == nil && u.Callback == nil) {
		http.Error(w, "invalid update", 400)
		return
	}
	var envelope map[string]json.RawMessage
	if e = json.Unmarshal(raw, &envelope); e != nil || envelope["update_id"] == nil {
		http.Error(w, "missing update id", 400)
		return
	}
	// Commit before acknowledging; retries cannot create a second queue entry.
	_, e = b.services.Storage.DB.ExecContext(ctx, "INSERT INTO webhook_updates(update_id,payload,received_at) VALUES(?,?,?) ON CONFLICT(update_id) DO NOTHING", u.ID, string(raw), time.Now().Unix())
	if e != nil {
		http.Error(w, "temporarily unavailable", 503)
		return
	}
	w.WriteHeader(200)
}

// Run processes the persisted inbox sequentially; dialog steps cannot race.
func (b *Bot) Run(ctx context.Context) {
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		var id int
		var raw string
		e := b.services.Storage.DB.QueryRowContext(ctx, "SELECT update_id,payload FROM webhook_updates WHERE processed_at IS NULL ORDER BY received_at,update_id LIMIT 1").Scan(&id, &raw)
		if errors.Is(e, sql.ErrNoRows) {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
			continue
		}
		if e != nil {
			b.logger.Error("inbox read failed")
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
			continue
		}
		var u tele.Update
		if e = json.Unmarshal([]byte(raw), &u); e != nil {
			b.logger.Error("invalid stored update", "update_id", id)
		} else {
			b.failed = false
			b.api.ProcessUpdate(u)
			if b.failed {
				select {
				case <-ctx.Done():
					return
				case <-time.After(time.Second):
				}
				continue
			}
		}
		ackCtx, cancelAck := context.WithTimeout(b.ctx, 3*time.Second)
		if _, e = b.services.Storage.DB.ExecContext(ackCtx, "UPDATE webhook_updates SET processed_at=?,payload='' WHERE update_id=?", time.Now().Unix(), id); e != nil {
			b.logger.Error("inbox acknowledgement failed", "update_id", id)
		}
		cancelAck()
	}
}

// CheckPublicURL verifies TLS and routing after the internal listener has started.
func CheckPublicURL(ctx context.Context, url string, client *http.Client) error {
	request, e := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(url, "/")+"/health", nil)
	if e != nil {
		return errors.New("WEBHOOK_URL is invalid")
	}
	response, e := client.Do(request)
	if e != nil {
		return errors.New("WEBHOOK_URL недоступен по HTTPS; проверьте туннель или reverse proxy")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return errors.New("WEBHOOK_URL недоступен: публичный /health должен возвращать 200")
	}
	return nil
}
