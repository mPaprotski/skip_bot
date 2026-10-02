package telegram

import (
	"context"
	"errors"
	"fmt"
	"html"
	"strings"
	"time"

	"github.com/poseshaemost/skip-bot/internal/service"
	"github.com/poseshaemost/skip-bot/internal/storage/sqlite"
	tele "gopkg.in/telebot.v3"
)

type button struct{ text, data string }

var menuActions = map[string]string{
	"🙋 Меня не будет": "absent:0", "📋 Моя история": "history:0",
	"📅 Расписание": "view:0", "📚 Предметы": "subjects:0",
	"⚙️ Настройки": "settings", "❓ Помощь": "help",
	"⚡ Панель администратора": "admin",
}

func btn(text, data string) button {
	labels := map[string]string{"Назад": "◀️ Назад", "Отмена": "❌ Отмена", "Меню": "🏠 Главное меню", "Главное меню": "🏠 Главное меню", "Предметы": "📚 Предметы", "Расписание": "📅 Расписание", "Добавить предмет": "➕ Добавить предмет", "Добавить занятие": "➕ Добавить занятие", "Код приглашения": "🔑 Код приглашения", "Статистика": "📊 Статистика", "Доступ и роли": "👥 Студенты и доступ", "Уважительная": "✅ Уважительная", "Неуважительная": "❌ Неуважительная", "Без комментария": "⏭ Пропустить", "Сохранить": "💾 Сохранить", "Переименовать": "✏️ Переименовать", "Архивировать": "📦 Архивировать", "Удалить": "🗑 Удалить", "Изменить все поля": "✏️ Изменить занятие", "История": "📋 Моя история", "Сегодня": "📅 Сегодня", "Все предметы": "📚 Все предметы", "Предыдущая страница": "⬅️ Назад", "Следующая страница": "Вперёд ➡️"}
	if label, ok := labels[text]; ok {
		text = label
	}
	return button{text, data}
}
func mainMenuKeyboard(admin bool) *tele.ReplyMarkup {
	m := &tele.ReplyMarkup{ResizeKeyboard: true, IsPersistent: true}
	rows := []tele.Row{
		m.Row(m.Text("🙋 Меня не будет"), m.Text("📋 Моя история")),
		m.Row(m.Text("📅 Расписание"), m.Text("📚 Предметы")),
		m.Row(m.Text("⚙️ Настройки"), m.Text("❓ Помощь")),
	}
	if admin {
		rows = append(rows, m.Row(m.Text("⚡ Панель администратора")))
	}
	m.Reply(rows...)
	return m
}
func inlineKeyboard(buttons []button) *tele.ReplyMarkup {
	m := &tele.ReplyMarkup{}
	var rows []tele.Row
	var controls []tele.Btn
	for i := 0; i < len(buttons); i++ {
		v := buttons[i]
		makeBtn := func(v button) tele.Btn {
			r := []rune(v.text)
			if len(r) > 45 {
				v.text = string(r[:44]) + "…"
			}
			return m.Data(v.text, "action", v.data)
		}
		if v.data == "back" || v.data == "cancel" || v.data == "menu" {
			controls = append(controls, makeBtn(v))
			continue
		}
		width := 1
		if paginationButton(v) {
			width = 3
		}
		if strings.HasPrefix(v.data, "day:") {
			width = 3
		} else if strings.HasPrefix(v.data, "reason:") || v.data == "schedule:0" || v.data == "members:0" || v.data == "continue" || strings.HasPrefix(v.data, "change:") {
			width = 2
		}
		row := tele.Row{makeBtn(v)}
		for width > 1 && i+1 < len(buttons) {
			next := buttons[i+1]
			pair := paginationButton(v) && paginationButton(next) || strings.HasPrefix(v.data, "day:") && strings.HasPrefix(next.data, "day:") || strings.HasPrefix(v.data, "reason:") && strings.HasPrefix(next.data, "reason:") || v.data == "schedule:0" && next.data == "subjects:0" || v.data == "members:0" && next.data == "invite" || v.data == "continue" && next.data == "cancel" || strings.HasPrefix(v.data, "change:") && strings.HasPrefix(next.data, "unmark:")
			if !pair {
				break
			}
			i++
			row = append(row, makeBtn(next))
			width--
		}
		rows = append(rows, row)
	}
	if len(controls) > 0 {
		rows = append(rows, tele.Row(controls))
	}
	m.Inline(rows...)
	return m
}

// Escape all content before applying HTML to the title, including user names,
// subject names and comments. Callback navigation updates the existing card.
func screenText(text string) string {
	heading, body, found := strings.Cut(text, "\n")
	title := "<b>" + html.EscapeString(heading) + "</b>"
	if found {
		title += "\n" + html.EscapeString(body)
	}
	return title
}
func textChunks(text string) []string {
	runes := []rune(text)
	var parts []string
	for len(runes) > 0 {
		units, end := 0, 0
		for end < len(runes) {
			width := 1
			if runes[end] > 0xffff {
				width = 2
			}
			if units+width > 3500 {
				break
			}
			units += width
			end++
		}
		parts = append(parts, string(runes[:end]))
		runes = runes[end:]
	}
	if len(parts) == 0 {
		return []string{" "}
	}
	return parts
}
func (b *Bot) send(c tele.Context, text string, buttons ...button) error {
	chunks := textChunks(text)
	m := inlineKeyboard(buttons)
	if len(chunks) == 1 && c.Callback() != nil && c.Callback().Message != nil {
		e := c.Edit(screenText(text), m, tele.ModeHTML)
		if e == nil || errors.Is(e, tele.ErrMessageNotModified) || errors.Is(e, tele.ErrSameMessageContent) {
			return nil
		}
	}
	for i, part := range chunks {
		opts := []interface{}{tele.ModeHTML}
		if i == len(chunks)-1 {
			opts = append(opts, m)
		}
		if e := c.Send(screenText(part), opts...); e != nil {
			return e
		}
	}
	return nil
}
func (b *Bot) menu(c tele.Context, u *sqlite.User) error {
	ctx, cancel := context.WithTimeout(b.ctx, 20*time.Second)
	defer cancel()
	g, e := b.services.Group(ctx, u.ID)
	if e != nil {
		return e
	}
	text := fmt.Sprintf("👋 Здравствуйте, %s!\n\n👥 Группа: %s\n🕒 Часовой пояс: %s\n\nВыберите раздел в меню ниже.\nОтметки можно менять только до начала пары.", u.FirstName, g.Name, g.Timezone)
	return c.Send(screenText(text), mainMenuKeyboard(u.Role == "admin" || u.Role == "owner"), tele.ModeHTML)
}
func (b *Bot) unfinished(c tele.Context, d *service.Draft) error {
	name := map[string]string{"schedule": "редактирование расписания", "subject": "редактирование предмета", "absent": "отметка отсутствия", "stats": "просмотр статистики"}[d.Kind]
	if name == "" {
		name = "настройка группы"
	}
	return b.send(c, "⚠️ Незавершённое действие\n\nВы начали: "+name+".\nПродолжить или отменить?", btn("▶️ Продолжить", "continue"), btn("❌ Отменить", "cancel"))
}

func paginationButton(v button) bool {
	return v.data == "noop" || v.text == "⬅️ Назад" || v.text == "Вперёд ➡️"
}
