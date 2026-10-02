package telegram

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/poseshaemost/skip-bot/internal/config"
	"github.com/poseshaemost/skip-bot/internal/service"
	"github.com/poseshaemost/skip-bot/internal/storage/sqlite"
	"github.com/poseshaemost/skip-bot/migrations"
	"github.com/poseshaemost/skip-bot/pkg/migrate"
	tele "gopkg.in/telebot.v3"
)

func testBot(t *testing.T) (*Bot, *[]string) {
	t.Helper()
	store, e := sqlite.New(filepath.Join(t.TempDir(), "bot.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { store.Close() })
	list, e := migrate.LoadMigrationsFromFS(migrations.Files, ".")
	if e != nil {
		t.Fatal(e)
	}
	if e = migrate.NewMigrator(store.DB, list).Migrate(context.Background()); e != nil {
		t.Fatal(e)
	}
	messages := []string{}
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		w := httptest.NewRecorder()
		raw, _ := io.ReadAll(r.Body)
		var req map[string]interface{}
		_ = json.Unmarshal(raw, &req)
		if text, ok := req["text"].(string); ok {
			messages = append(messages, text)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":1,"chat":{"id":1,"type":"private"}}}`))
		return w.Result(), nil
	})
	api, e := tele.NewBot(tele.Settings{Token: "fake", URL: "https://telegram.test", Offline: true, Synchronous: true, Client: &http.Client{Transport: transport}})
	if e != nil {
		t.Fatal(e)
	}
	b := &Bot{api: api, services: service.NewServices(store, 1, "Europe/Minsk"), config: &config.Config{OwnerTelegramID: 1, WebhookPathSecret: "path", WebhookSecretToken: "header"}, logger: slog.New(slog.NewTextHandler(io.Discard, nil)), ctx: context.Background()}
	b.services.Now = func() time.Time { return time.Date(2026, 10, 2, 5, 0, 0, 0, time.UTC) }
	return b, &messages
}
func TestWebhookValidationAndDeduplication(t *testing.T) {
	b, _ := testBot(t)
	handler := b.Handler()
	cases := []struct {
		method, secret, body string
		code                 int
	}{{"GET", "header", "", 405}, {"POST", "", "{}", 403}, {"POST", "bad", "{}", 403}, {"POST", "header", "{", 400}, {"POST", "header", `{"message":{}}`, 400}, {"POST", "header", strings.Repeat("a", (1<<20)+1), 413}, {"POST", "header", `{"update_id":10,"message":{"message_id":1}}`, 200}, {"POST", "header", `{"update_id":10,"message":{"message_id":1}}`, 200}}
	for _, v := range cases {
		r := httptest.NewRequest(v.method, "/webhook/path", strings.NewReader(v.body))
		r.Header.Set("X-Telegram-Bot-Api-Secret-Token", v.secret)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != v.code {
			t.Fatalf("expected %d got %d: %s", v.code, w.Code, w.Body.String())
		}
	}
	var count int
	if e := b.services.Storage.DB.QueryRow("SELECT count(*) FROM webhook_updates").Scan(&count); e != nil || count != 1 {
		t.Fatalf("dedup count=%d err=%v", count, e)
	}
}
func TestPrivateChatAndEndToEndDialogs(t *testing.T) {
	b, messages := testBot(t)
	id := 0
	send := func(uid int64, text, action string) {
		t.Helper()
		id++
		u := &tele.User{ID: uid, FirstName: "Студент"}
		chat := &tele.Chat{ID: uid, Type: tele.ChatPrivate}
		update := tele.Update{ID: id}
		if action == "" {
			update.Message = &tele.Message{ID: id, Sender: u, Chat: chat, Text: text}
		} else {
			update.Callback = &tele.Callback{ID: "cb", Sender: u, Message: &tele.Message{ID: id, Chat: chat}, Data: "\faction|" + action}
		}
		if e := b.handle(b.api.NewContext(update)); e != nil {
			t.Fatal(e)
		}
	}
	send(1, "/start", "")
	send(1, "Группа", "")
	send(1, "", "subject_add")
	send(1, "Математика", "")
	send(1, "", "schedule_add")
	subs, e := b.services.Subjects(context.Background(), 1)
	if e != nil {
		t.Fatal(e)
	}
	send(1, "", "pick:"+fmtInt(subs[0].ID))
	send(1, "", "day:5")
	send(1, "1", "")
	send(1, "09:00", "")
	lessons, e := b.services.Schedules(context.Background(), 1, "")
	if e != nil || len(lessons) != 0 {
		t.Fatal("saved without confirmation")
	}
	send(1, "", "back")
	send(1, "09:00", "")
	send(1, "", "confirm")
	lessons, e = b.services.Schedules(context.Background(), 1, "")
	if e != nil || len(lessons) != 1 {
		t.Fatal("schedule missing")
	}
	code, e := b.services.Invite(context.Background(), 1, false)
	if e != nil {
		t.Fatal(e)
	}
	send(2, "/start", "")
	send(2, code, "")
	send(2, "", "absent:0")
	send(2, "", "mark:"+fmtInt(lessons[0].ID)+":2026-10-02")
	send(2, "", "reason:valid")
	send(2, "", "skip")
	history, e := b.services.History(context.Background(), 2, 0)
	if e != nil || len(history) != 1 {
		t.Fatal("absence missing")
	}
	if len(*messages) == 0 || !strings.Contains((*messages)[len(*messages)-1], "не переноси эту Н") {
		t.Fatalf("missing required confirmation: %v", *messages)
	}
	// Today's report includes every subject directly, without a subject picker.
	ctx := context.Background()
	if e = b.services.SaveSubject(ctx, 1, 0, "Физика", false); e != nil {
		t.Fatal(e)
	}
	subs, e = b.services.Subjects(ctx, 1)
	if e != nil {
		t.Fatal(e)
	}
	var physicsID int64
	for _, sub := range subs {
		if sub.Name == "Физика" {
			physicsID = sub.ID
		}
	}
	friday := 5
	physics := &sqlite.Schedule{SubjectID: physicsID, DayOfWeek: &friday, PairNumber: 2, StartTime: "10:00"}
	if e = b.services.SaveSchedule(ctx, 1, physics); e != nil {
		t.Fatal(e)
	}
	if e = b.services.Mark(ctx, 2, physics.ID, "2026-10-02", "invalid", ""); e != nil {
		t.Fatal(e)
	}
	if e = b.services.Mark(ctx, 2, physics.ID, "2026-10-09", "valid", ""); e != nil {
		t.Fatal(e)
	}
	send(1, "/group", "")
	send(1, "", "stats_today")
	report := (*messages)[len(*messages)-1]
	if !strings.Contains(report, "Всего: 2") || !strings.Contains(report, "Математика") || !strings.Contains(report, "Физика") || strings.Contains(report, "Выберите предмет") {
		t.Fatalf("today must directly show all today's absences: %s", report)
	}
	draft, e := b.services.Draft(ctx, 1)
	if e != nil || draft == nil || draft.Step != 3 || draft.Subject != 0 || draft.Date != "2026-10-02" || draft.Page != 0 {
		t.Fatalf("incorrect today report state: %+v %v", draft, e)
	}
	if !strings.Contains((*messages)[len(*messages)-1], "уважительных: 1") {
		t.Fatal("stats missing")
	}
	before := len(*messages)
	update := tele.Update{ID: 100, Message: &tele.Message{Sender: &tele.User{ID: 2}, Chat: &tele.Chat{ID: -1, Type: tele.ChatGroup}, Text: "/settings"}}
	if e = b.handle(b.api.NewContext(update)); e != nil {
		t.Fatal(e)
	}
	if len(*messages) != before {
		t.Fatal("group data exposed in group chat")
	}
}
func fmtInt(n int64) string { return strconv.FormatInt(n, 10) }

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestPublicURLProbe(t *testing.T) {
	for _, code := range []int{200, 403, 502} {
		client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.String() != "https://bot.example.com/health" {
				t.Fatal("wrong probe URL")
			}
			w := httptest.NewRecorder()
			w.WriteHeader(code)
			return w.Result(), nil
		})}
		e := CheckPublicURL(context.Background(), "https://bot.example.com", client)
		if (e == nil) != (code == 200) {
			t.Fatalf("status=%d err=%v", code, e)
		}
	}
}
func TestPersistedWebhookWorker(t *testing.T) {
	b, _ := testBot(t)
	b.api.Handle("/start", b.handle)
	raw := `{"update_id":42,"message":{"message_id":1,"from":{"id":1,"first_name":"Owner"},"chat":{"id":1,"type":"private"},"text":"/start"}}`
	r := httptest.NewRequest("POST", "/webhook/path", strings.NewReader(raw))
	r.Header.Set("X-Telegram-Bot-Api-Secret-Token", "header")
	w := httptest.NewRecorder()
	b.Handler().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); b.Run(ctx) }()
	defer func() { cancel(); <-done }()
	deadline := time.After(3 * time.Second)
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		var count int
		e := b.services.Storage.DB.QueryRow("SELECT count(*) FROM webhook_updates WHERE processed_at IS NOT NULL AND payload=''").Scan(&count)
		if e != nil {
			t.Fatal(e)
		}
		if count == 1 {
			draft, e := b.services.Draft(context.Background(), 1)
			if e != nil || draft == nil || draft.Kind != "setup" {
				t.Fatal("queued update was not handled")
			}
			return
		}
		select {
		case <-deadline:
			t.Fatal("worker did not process persisted inbox")
		case <-tick.C:
		}
	}
}
