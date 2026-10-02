package telegram

import (
	"context"
	"encoding/json"
	tele "gopkg.in/telebot.v3"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMenuKeyboardAndInlineControls(t *testing.T) {
	student := mainMenuKeyboard(false)
	admin := mainMenuKeyboard(true)
	if !student.IsPersistent || !student.ResizeKeyboard || len(student.ReplyKeyboard) != 3 || len(admin.ReplyKeyboard) != 4 {
		t.Fatal("persistent role-aware menu missing")
	}
	for _, row := range student.ReplyKeyboard {
		for _, button := range row {
			if _, ok := menuActions[button.Text]; !ok {
				t.Fatal("unroutable menu button")
			}
		}
	}
	controls := inlineKeyboard(append(nav(), btn("Уважительная", "reason:valid"), btn("Неуважительная", "reason:invalid")))
	if len(controls.InlineKeyboard) != 2 || len(controls.InlineKeyboard[0]) != 2 || len(controls.InlineKeyboard[1]) != 2 {
		t.Fatal("reasons and controls should be separate compact rows")
	}
	if !strings.Contains(controls.InlineKeyboard[1][0].Data, "back") {
		t.Fatal("controls should be below choices")
	}
	pagination := inlineKeyboard(pages("history", 1, true))
	if len(pagination.InlineKeyboard[0]) != 3 {
		t.Fatal("pagination should share one row")
	}
}
func TestMenuRoutingKeepsDraftAndEscapesContent(t *testing.T) {
	b, messages := testBot(t)
	ctx := context.Background()
	send := func(text, action string) {
		t.Helper()
		u := &tele.User{ID: 1, FirstName: "<Иван>"}
		chat := &tele.Chat{ID: 1, Type: tele.ChatPrivate}
		update := tele.Update{ID: 1}
		if action == "" {
			update.Message = &tele.Message{ID: 1, Sender: u, Chat: chat, Text: text}
		} else {
			update.Callback = &tele.Callback{ID: "cb", Sender: u, Message: &tele.Message{ID: 1, Chat: chat}, Data: "\faction|" + action}
		}
		if e := b.handle(b.api.NewContext(update)); e != nil {
			t.Fatal(e)
		}
	}
	send("/start", "")
	send("Группа <А>", "")
	send("", "menu")
	if !strings.Contains((*messages)[len(*messages)-1], "&lt;Иван&gt;") || !strings.Contains((*messages)[len(*messages)-1], "&lt;А&gt;") {
		t.Fatal("user content not escaped")
	}
	send("📅 Расписание", "")
	if !strings.Contains((*messages)[len(*messages)-1], "Пока нет записей") {
		t.Fatal("reply keyboard did not route to schedule")
	}
	send("", "subject_add")
	send("📅 Расписание", "")
	d, e := b.services.Draft(ctx, 1)
	if e != nil || d == nil || d.Kind != "subject" {
		t.Fatal("menu click consumed or cleared draft")
	}
	if !strings.Contains((*messages)[len(*messages)-1], "Незавершённое действие") {
		t.Fatal("resume prompt missing")
	}
	send("", "continue")
	send("Математика <1>", "")
	subjects, e := b.services.Subjects(ctx, 1)
	if e != nil || len(subjects) != 1 || subjects[0].Name != "Математика <1>" {
		t.Fatal("draft could not be resumed")
	}
	if strings.Contains(screenText("Заголовок\n<script>&"), "<script>") {
		t.Fatal("HTML injection")
	}
}
func TestCallbackEditsCard(t *testing.T) {
	b, _ := testBot(t)
	var requests []string
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests = append(requests, r.URL.Path)
		raw, e := io.ReadAll(r.Body)
		if e != nil {
			t.Fatal(e)
		}
		var body map[string]interface{}
		if e = json.Unmarshal(raw, &body); e != nil {
			t.Fatal(e)
		}
		if body["message_id"] != float64(99) && body["message_id"] != "99" {
			t.Fatalf("wrong message id: %v", body)
		}
		if body["text"] != "<b>📚 Карточка</b>\nТекст" || body["parse_mode"] != "HTML" {
			t.Fatal("missing formatted card")
		}
		w := httptest.NewRecorder()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":99,"chat":{"id":1,"type":"private"}}}`))
		return w.Result(), nil
	})}
	api, e := tele.NewBot(tele.Settings{Token: "fake", URL: "https://telegram.test", Offline: true, Synchronous: true, Client: client})
	if e != nil {
		t.Fatal(e)
	}
	b.api = api
	c := api.NewContext(tele.Update{ID: 1, Callback: &tele.Callback{ID: "cb", Sender: &tele.User{ID: 1}, Message: &tele.Message{ID: 99, Chat: &tele.Chat{ID: 1, Type: tele.ChatPrivate}}}})
	if e = b.send(c, "📚 Карточка\nТекст", btn("Назад", "menu")); e != nil {
		t.Fatal(e)
	}
	if len(requests) != 1 || !strings.HasSuffix(requests[0], "/editMessageText") {
		t.Fatalf("callback should edit instead of sending: %v", requests)
	}
}
