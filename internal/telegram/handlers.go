package telegram

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/poseshaemost/skip-bot/internal/service"
	"github.com/poseshaemost/skip-bot/internal/storage/sqlite"
	tele "gopkg.in/telebot.v3"
)

func (b *Bot) handle(c tele.Context) error {
	ctx, cancel := context.WithTimeout(b.ctx, 20*time.Second)
	defer cancel()
	if c.Sender() == nil {
		return nil
	}
	// Group data and invite codes are only shown in the user's private chat.
	if c.Chat() == nil || c.Chat().Type != tele.ChatPrivate {
		return nil
	}
	if c.Callback() != nil {
		_ = c.Respond()
	}
	u, e := b.services.Register(ctx, c.Sender().ID, c.Sender().Username, c.Sender().FirstName, c.Sender().LastName)
	if e != nil {
		return e
	}
	e = b.route(ctx, c, u)
	if e != nil {
		if errors.Is(e, context.Canceled) {
			return e
		}
		b.logger.Error("update failed", "update_id", c.Update().ID, "user_id", u.ID)
		message := e.Error()
		var sqlError interface{ Code() int }
		var parseError *strconv.NumError
		if errors.Is(e, sql.ErrNoRows) {
			message = "Запись не найдена. Откройте меню заново."
		} else if errors.As(e, &sqlError) {
			message = "Не удалось сохранить данные: запись уже существует или поля некорректны."
		} else if errors.As(e, &parseError) {
			message = "Некорректная кнопка. Откройте меню заново."
		} else if errors.Is(e, context.DeadlineExceeded) {
			message = "Время обработки истекло. Попробуйте ещё раз."
		}
		return b.send(c, message, btn("Главное меню", "menu"))
	}
	return nil
}
func (b *Bot) route(ctx context.Context, c tele.Context, u *sqlite.User) error {
	action := ""
	text := strings.TrimSpace(c.Text())
	if cb := c.Callback(); cb != nil {
		action = strings.TrimPrefix(cb.Data, "\faction|")
		if action == cb.Data {
			action = strings.TrimPrefix(cb.Data, "action|")
		}
	}
	if strings.HasPrefix(text, "/") {
		command := strings.SplitN(strings.Fields(text)[0], "@", 2)[0]
		switch command {
		case "/start":
			action = "start"
		case "/help":
			action = "help"
		case "/absent":
			action = "absent:0"
		case "/mystats":
			action = "history:0"
		case "/settings":
			action = "settings"
		case "/admin":
			action = "admin"
		case "/group":
			action = "stats"
		case "/cancel":
			action = "cancel"
		default:
			return b.send(c, "Неизвестная команда. Используйте /help.")
		}
	}
	if action == "" && c.Callback() == nil {
		if target, ok := menuActions[text]; ok {
			action = target
			if u.GroupID != nil && u.IsActive {
				d, e := b.services.Draft(ctx, u.ID)
				if e != nil {
					return e
				}
				if d != nil {
					return b.unfinished(c, d)
				}
			}
		}
	}
	if action == "help" {
		return b.send(c, "/start — подключение и меню\n/absent — отсутствие\n/mystats — история\n/settings — настройки\n/admin — управление\n/group — статистика\n/cancel — отменить диалог\n\nОтметки можно создавать, менять и отменять только до начала пары. Уважительная причина допускает комментарий. Время указано в часовом поясе группы.")
	}
	if action == "start" {
		if !u.IsActive {
			return service.ErrAccess
		}
		if u.GroupID == nil {
			if u.TelegramID == b.config.OwnerTelegramID {
				if e := b.services.SaveDraft(ctx, u.ID, &service.Draft{Kind: "setup"}); e != nil {
					return e
				}
				return b.send(c, "👑 Настройка группы\n\nВведите название вашей учебной группы.")
			}
			return b.send(c, "👋 Добро пожаловать в бот посещаемости!\n\nЗдесь можно посмотреть расписание и отметить отсутствие до начала пары.\n\n🔑 Введите код приглашения, полученный у старосты.")
		}
		d, e := b.services.Draft(ctx, u.ID)
		if e != nil {
			return e
		}
		if d != nil {
			return b.unfinished(c, d)
		}
		return b.menu(c, u)
	}
	if !u.IsActive {
		return service.ErrAccess
	}
	if u.GroupID == nil {
		d, e := b.services.Draft(ctx, u.ID)
		if e != nil {
			return e
		}
		if d != nil && d.Kind == "setup" && text != "" {
			if e := b.services.Setup(ctx, u.ID, text); e != nil {
				return e
			}
			if e := b.services.ClearDraft(ctx, u.ID); e != nil {
				return e
			}
			return b.send(c, "Группа создана. Код приглашения доступен в /admin.", btn("Меню", "menu"))
		}
		if text != "" {
			if e := b.services.Join(ctx, u.ID, text); e != nil {
				return e
			}
			u, e = b.services.User(ctx, u.TelegramID)
			if e != nil {
				return e
			}
			return b.menu(c, u)
		}
		return b.send(c, "Сначала выполните /start и подключитесь к группе.")
	}
	if _, e := b.services.Member(ctx, u.ID, false); e != nil {
		return e
	}
	if action == "" {
		d, e := b.services.Draft(ctx, u.ID)
		if e != nil {
			return e
		}
		if d == nil {
			return b.send(c, "Используйте /start или /help.")
		}
		return b.draftText(ctx, c, u, d, text)
	}
	parts := strings.Split(action, ":")
	key := parts[0]
	arg := func(n int) (int64, error) {
		if len(parts) <= n {
			return 0, errors.New("Кнопка устарела.")
		}
		return strconv.ParseInt(parts[n], 10, 64)
	}
	switch key {
	case "noop":
		return nil
	case "continue":
		d, e := b.services.Draft(ctx, u.ID)
		if e != nil {
			return e
		}
		if d == nil {
			return b.menu(c, u)
		}
		return b.renderDraft(ctx, c, u, d)
	case "menu":
		return b.menu(c, u)
	case "cancel":
		if e := b.services.ClearDraft(ctx, u.ID); e != nil {
			return e
		}
		return b.menu(c, u)
	case "settings":
		g, e := b.services.Group(ctx, u.ID)
		if e != nil {
			return e
		}
		return b.send(c, fmt.Sprintf("⚙️ Настройки\n\n👥 Группа: %s\n🕒 Часовой пояс: %s\n👤 Роль: %s", g.Name, g.Timezone, map[string]string{"student": "студент", "admin": "администратор", "owner": "владелец"}[u.Role]), btn("Меню", "menu"))
	case "admin":
		if _, e := b.services.Member(ctx, u.ID, true); e != nil {
			return e
		}
		return b.send(c, "⚡ Панель администратора\n\nУправляйте расписанием, предметами и доступом к группе.", btn("Добавить занятие", "schedule_add"), btn("Расписание", "schedule:0"), btn("Управление расписанием", "manage_schedule:0"), btn("Предметы", "subjects:0"), btn("Добавить предмет", "subject_add"), btn("Доступ и роли", "members:0"), btn("Код приглашения", "invite"), btn("Статистика", "stats"), btn("Меню", "menu"))
	case "invite", "rotate":
		code, e := b.services.Invite(ctx, u.ID, key == "rotate")
		if e != nil {
			return e
		}
		return b.send(c, "🔑 Код приглашения\n\n"+code+"\n\nПередайте этот код студентам для подключения к группе.", btn("Заменить код", "rotate"), btn("Назад", "admin"))
	case "subjects":
		page, e := arg(1)
		if e != nil {
			return e
		}
		list, e := b.services.Subjects(ctx, u.ID)
		if e != nil {
			return e
		}
		items := []button{}
		start, end, e := bounds(len(list), page)
		if e != nil {
			return e
		}
		for _, sub := range list[start:end] {
			if u.Role != "student" {
				items = append(items, btn(sub.Name, fmt.Sprintf("subject:%d", sub.ID)))
			} else {
				items = append(items, btn(sub.Name, fmt.Sprintf("subject_view:%d", sub.ID)))
			}
		}
		items = append(items, pages("subjects", page, end < len(list))...)
		return b.send(c, empty("📚 Предметы", len(list)), items...)
	case "subject_view":
		id, e := arg(1)
		if e != nil {
			return e
		}
		sub, e := b.services.Subject(ctx, u.ID, id)
		if e != nil {
			return e
		}
		return b.send(c, "📚 "+sub.Name+"\n\nПосмотреть занятия или отметить отсутствие можно в разделах ниже.", btn("Расписание", "view:0"), btn("🙋 Меня не будет", "absent:0"), btn("Назад", "subjects:0"))
	case "subject":
		if _, e := b.services.Member(ctx, u.ID, true); e != nil {
			return e
		}
		id, e := arg(1)
		if e != nil {
			return e
		}
		sub, e := b.services.Subject(ctx, u.ID, id)
		if e != nil {
			return e
		}
		return b.send(c, sub.Name, btn("Переименовать", fmt.Sprintf("rename:%d", id)), btn("Архивировать", fmt.Sprintf("archive:%d", id)), btn("Назад", "subjects:0"))
	case "subject_add", "rename":
		d := &service.Draft{Kind: "subject"}
		if key == "rename" {
			id, e := arg(1)
			if e != nil {
				return e
			}
			if _, e = b.services.Subject(ctx, u.ID, id); e != nil {
				return e
			}
			d.Target = id
		}
		if e := b.services.SaveDraft(ctx, u.ID, d); e != nil {
			return e
		}
		return b.renderDraft(ctx, c, u, d)
	case "archive":
		id, e := arg(1)
		if e != nil {
			return e
		}
		if e = b.services.SaveSubject(ctx, u.ID, id, "", true); e != nil {
			return e
		}
		return b.send(c, "Предмет архивирован.", btn("Назад", "subjects:0"))
	case "view", "schedule":
		if _, e := b.services.Member(ctx, u.ID, false); e != nil {
			return e
		}
		items := []button{}
		for _, day := range []int{1, 2, 3, 4, 5, 6, 0} {
			items = append(items, btn(scheduleWeekdays[day], fmt.Sprintf("schedule_day:%d:0", day)))
		}
		items = append(items, btn("Главное меню", "menu"))
		return b.send(c, "📅 Расписание", items...)
	case "schedule_day":
		day, e := arg(1)
		if e != nil {
			return e
		}
		page, e := arg(2)
		if e != nil {
			return e
		}
		items, e := b.scheduleDayButtons(ctx, u.ID, day, page)
		if e != nil {
			return e
		}
		return b.send(c, "📅 Расписание", items...)
	case "manage_schedule":
		page, e := arg(1)
		if e != nil {
			return e
		}
		if key == "manage_schedule" {
			if _, e = b.services.Member(ctx, u.ID, true); e != nil {
				return e
			}
		}
		list, e := b.services.Schedules(ctx, u.ID, "")
		if e != nil {
			return e
		}
		start, end, e := bounds(len(list), page)
		if e != nil {
			return e
		}
		var out strings.Builder
		items := []button{}
		for _, t := range list[start:end] {
			desc := b.services.Describe(ctx, u.ID, t)
			out.WriteString(desc)
			out.WriteByte('\n')
			if key == "manage_schedule" {
				items = append(items, btn(desc, fmt.Sprintf("lesson:%d", t.ID)))
			}
		}
		items = append(items, pages(key, page, end < len(list))...)
		return b.send(c, empty("📅 Расписание\n\n"+out.String(), len(list)), items...)
	case "lesson":
		if _, e := b.services.Member(ctx, u.ID, true); e != nil {
			return e
		}
		id, e := arg(1)
		if e != nil {
			return e
		}
		t, e := b.services.Schedule(ctx, u.ID, id)
		if e != nil {
			return e
		}
		return b.send(c, b.services.Describe(ctx, u.ID, t), btn("Изменить все поля", fmt.Sprintf("edit:%d", id)), btn("Удалить", fmt.Sprintf("delete_ask:%d", id)), btn("Назад", "manage_schedule:0"))
	case "delete_ask":
		if _, e := b.services.Member(ctx, u.ID, true); e != nil {
			return e
		}
		id, e := arg(1)
		if e != nil {
			return e
		}
		t, e := b.services.Schedule(ctx, u.ID, id)
		if e != nil {
			return e
		}
		return b.send(c, "Удалить занятие?\n"+b.services.Describe(ctx, u.ID, t), btn("Да, удалить", fmt.Sprintf("delete:%d", id)), btn("Назад", fmt.Sprintf("lesson:%d", id)))
	case "delete":
		id, e := arg(1)
		if e != nil {
			return e
		}
		if e = b.services.DeleteSchedule(ctx, u.ID, id); e != nil {
			return e
		}
		return b.send(c, "Занятие удалено.", btn("Назад", "manage_schedule:0"))
	case "schedule_add", "edit":
		d := &service.Draft{Kind: "schedule", Step: 1}
		if key == "edit" {
			id, e := arg(1)
			if e != nil {
				return e
			}
			t, e := b.services.Schedule(ctx, u.ID, id)
			if e != nil {
				return e
			}
			d.Schedule = *t
		}
		if e := b.services.SaveDraft(ctx, u.ID, d); e != nil {
			return e
		}
		return b.renderDraft(ctx, c, u, d)
	case "absent":
		page, e := arg(1)
		if e != nil {
			return e
		}
		date := b.services.Today(ctx, u.ID)
		list, e := b.services.Schedules(ctx, u.ID, date)
		if e != nil {
			return e
		}
		var active []*sqlite.Schedule
		for _, t := range list {
			sub, e := b.services.Subject(ctx, u.ID, t.SubjectID)
			if e != nil {
				return e
			}
			start, e := b.services.Start(ctx, u.ID, t, date)
			if e == nil && !sub.IsArchived && b.services.Now().Before(start) {
				active = append(active, t)
			}
		}
		start, end, e := bounds(len(active), page)
		if e != nil {
			return e
		}
		items := []button{}
		for _, t := range active[start:end] {
			items = append(items, btn(b.services.Describe(ctx, u.ID, t), fmt.Sprintf("mark:%d:%s", t.ID, date)))
		}
		items = append(items, pages("absent", page, end < len(active))...)
		return b.send(c, empty("🙋 Меня не будет\n\nВыберите занятие на сегодня. Отметка доступна только до начала пары.", len(active)), items...)
	case "mark", "change":
		d := &service.Draft{Kind: "absent", Step: 1}
		id, e := arg(1)
		if e != nil {
			return e
		}
		if key == "change" {
			a, e := b.services.Absence(ctx, u.ID, id)
			if e != nil {
				return e
			}
			if a.ScheduleID == nil {
				return service.ErrDeadline
			}
			d.Schedule.ID = *a.ScheduleID
			d.Date = a.AbsenceDate
		} else {
			if len(parts) != 3 {
				return errors.New("Кнопка устарела.")
			}
			d.Schedule.ID = id
			d.Date = parts[2]
		}
		t, e := b.services.Schedule(ctx, u.ID, d.Schedule.ID)
		if e != nil {
			return e
		}
		start, e := b.services.Start(ctx, u.ID, t, d.Date)
		if e != nil {
			return e
		}
		if !b.services.Now().Before(start) {
			return service.ErrDeadline
		}
		if e = b.services.SaveDraft(ctx, u.ID, d); e != nil {
			return e
		}
		return b.renderDraft(ctx, c, u, d)
	case "history":
		page, e := arg(1)
		if e != nil {
			return e
		}
		list, e := b.services.History(ctx, u.ID, int(page))
		if e != nil {
			return e
		}
		items := []button{}
		var out strings.Builder
		for _, a := range list {
			sub, e := b.services.Subject(ctx, u.ID, a.SubjectID)
			if e != nil {
				return e
			}
			out.WriteString(fmt.Sprintf("%s · %s · пара %d · %s\n", a.AbsenceDate, sub.Name, a.PairNumber, reason(a.ReasonType)))
			if a.ScheduleID != nil {
				items = append(items, btn(fmt.Sprintf("Изменить: %s, пара %d", a.AbsenceDate, a.PairNumber), fmt.Sprintf("change:%d", a.ID)), btn("Отменить отметку", fmt.Sprintf("unmark:%d", a.ID)))
			}
		}
		items = append(items, pages("history", page, len(list) == 10)...)
		return b.send(c, empty("📋 Моя история\n\n"+out.String(), len(list)), items...)
	case "unmark":
		id, e := arg(1)
		if e != nil {
			return e
		}
		if e = b.services.Cancel(ctx, u.ID, id); e != nil {
			return e
		}
		return b.send(c, "Отметка отменена.", btn("История", "history:0"))
	case "members":
		page, e := arg(1)
		if e != nil {
			return e
		}
		list, e := b.services.Members(ctx, u.ID)
		if e != nil {
			return e
		}
		start, end, e := bounds(len(list), page)
		if e != nil {
			return e
		}
		items := []button{}
		for _, m := range list[start:end] {
			status := m.Role
			if !m.IsActive {
				status = "доступ отозван"
			}
			items = append(items, btn(fmt.Sprintf("%s %s (%s)", m.FirstName, m.LastName, status), fmt.Sprintf("member:%d", m.ID)))
		}
		items = append(items, pages("members", page, end < len(list))...)
		return b.send(c, "👥 Студенты и доступ\n\nВыберите участника для управления доступом и ролью.", items...)
	case "member":
		list, e := b.services.Members(ctx, u.ID)
		if e != nil {
			return e
		}
		id, e := arg(1)
		if e != nil {
			return e
		}
		for _, m := range list {
			if m.ID == id {
				items := []button{btn("Отозвать доступ", fmt.Sprintf("user:revoke:%d", id)), btn("Восстановить доступ", fmt.Sprintf("user:restore:%d", id))}
				if u.Role == "owner" {
					items = append(items, btn("Назначить администратором", fmt.Sprintf("user:admin:%d", id)), btn("Снять администратора", fmt.Sprintf("user:student:%d", id)))
				}
				items = append(items, btn("Назад", "members:0"))
				return b.send(c, m.FirstName+" "+m.LastName, items...)
			}
		}
		return service.ErrAccess
	case "user":
		id, e := arg(2)
		if e != nil {
			return e
		}
		if e = b.services.ManageUser(ctx, u.ID, id, parts[1]); e != nil {
			return e
		}
		return b.send(c, "Доступ обновлён.", btn("Участники", "members:0"))
	case "stats":
		d := &service.Draft{Kind: "stats", Step: 1, Date: b.services.Today(ctx, u.ID)}
		if e := b.services.SaveDraft(ctx, u.ID, d); e != nil {
			return e
		}
		return b.renderDraft(ctx, c, u, d)
	case "back", "pick", "day", "reason", "skip", "confirm", "stats_today", "stats_all", "statpage":
		d, e := b.services.Draft(ctx, u.ID)
		if e != nil {
			return e
		}
		if d == nil {
			return errors.New("Диалог завершён. Начните заново через меню.")
		}
		return b.draftAction(ctx, c, u, d, parts)
	default:
		return b.send(c, "Кнопка устарела. Откройте /start.")
	}
}
func bounds(count int, page int64) (int, int, error) {
	if page < 0 || page > 100000 {
		return 0, 0, errors.New("Некорректная страница.")
	}
	start := int(page) * 10
	if start > count {
		start = count
	}
	end := start + 10
	if end > count {
		end = count
	}
	return start, end, nil
}
func pages(key string, page int64, more bool) []button {
	items := []button{}
	if page > 0 {
		items = append(items, btn("Предыдущая страница", fmt.Sprintf("%s:%d", key, page-1)))
	}
	items = append(items, btn(fmt.Sprintf("Стр. %d", page+1), "noop"))
	if more {
		items = append(items, btn("Следующая страница", fmt.Sprintf("%s:%d", key, page+1)))
	}
	return append(items, btn("Главное меню", "menu"))
}
func empty(title string, count int) string {
	if count == 0 {
		return title + "\n\nПока нет записей в этом разделе."
	}
	return title
}
func reason(r string) string {
	if r == "valid" {
		return "уважительная"
	}
	return "неуважительная"
}
func nav() []button { return []button{btn("Назад", "back"), btn("Отмена", "cancel")} }
func (b *Bot) renderDraft(ctx context.Context, c tele.Context, u *sqlite.User, d *service.Draft) error {
	items := nav()
	text := ""
	switch d.Kind {
	case "subject":
		text = "📚 Название предмета\n\nВведите название (от 1 до 100 символов)."
	case "schedule":
		switch d.Step {
		case 1:
			list, e := b.services.Subjects(ctx, u.ID)
			if e != nil {
				return e
			}
			start, end, e := bounds(len(list), int64(d.Page))
			if e != nil {
				return e
			}
			text = empty("Выберите предмет", len(list))
			for _, sub := range list[start:end] {
				items = append(items, btn(sub.Name, fmt.Sprintf("pick:%d", sub.ID)))
			}
			if d.Page > 0 {
				items = append(items, btn("Предыдущие предметы", fmt.Sprintf("statpage:%d", d.Page-1)))
			}
			if end < len(list) {
				items = append(items, btn("Следующие предметы", fmt.Sprintf("statpage:%d", d.Page+1)))
			}
		case 2:
			text = "Выберите день недели или введите конкретную дату ГГГГ-ММ-ДД."
			for i, name := range []string{"Вс", "Пн", "Вт", "Ср", "Чт", "Пт", "Сб"} {
				items = append(items, btn(name, fmt.Sprintf("day:%d", i)))
			}
		case 3:
			text = "Введите номер пары от 1 до 8."
		case 4:
			g, e := b.services.Group(ctx, u.ID)
			if e != nil {
				return e
			}
			text = "Введите время начала ЧЧ:ММ. Часовой пояс: " + g.Timezone
		case 5:
			g, e := b.services.Group(ctx, u.ID)
			if e != nil {
				return e
			}
			text = "Подтвердите сохранение:\n" + b.services.Describe(ctx, u.ID, &d.Schedule) + "\nЧасовой пояс: " + g.Timezone
			items = append(items, btn("Сохранить", "confirm"))
		}
	case "absent":
		if d.Step == 1 {
			lesson, e := b.services.Schedule(ctx, u.ID, d.Schedule.ID)
			if e != nil {
				return e
			}
			text = "🙋 Причина отсутствия\n\n" + b.services.Describe(ctx, u.ID, lesson) + "\nДата: " + d.Date + "\n\nВыберите подходящий вариант."
			items = append(items, btn("Уважительная", "reason:valid"), btn("Неуважительная", "reason:invalid"))
		} else {
			text = "💬 Комментарий\n\nДобавьте пояснение (до 500 символов) или нажмите «Пропустить»."
			items = append(items, btn("Без комментария", "skip"))
		}
	case "stats":
		switch d.Step {
		case 1:
			text = "Введите дату ГГГГ-ММ-ДД или выберите сегодня."
			items = append(items, btn("Сегодня", "stats_today"))
		case 2:
			text = "Выберите предмет или все предметы."
			list, e := b.services.Subjects(ctx, u.ID)
			if e != nil {
				return e
			}
			start, end, e := bounds(len(list), int64(d.Page))
			if e != nil {
				return e
			}
			for _, sub := range list[start:end] {
				items = append(items, btn(sub.Name, fmt.Sprintf("pick:%d", sub.ID)))
			}
			items = append(items, btn("Все предметы", "stats_all"))
			if d.Page > 0 {
				items = append(items, btn("Предыдущие предметы", fmt.Sprintf("statpage:%d", d.Page-1)))
			}
			if end < len(list) {
				items = append(items, btn("Следующие предметы", fmt.Sprintf("statpage:%d", d.Page+1)))
			}
		case 3:
			return b.stats(ctx, c, u, d)
		}
	default:
		return errors.New("Неизвестный диалог.")
	}
	if d.Kind == "schedule" {
		text = fmt.Sprintf("📅 %s · шаг %d из 5\n\n%s", map[bool]string{true: "Изменение занятия", false: "Новое занятие"}[d.Schedule.ID != 0], d.Step, text)
	}
	if d.Kind == "stats" {
		text = "📊 Статистика группы\n\n" + text
	}
	return b.send(c, text, items...)
}
func (b *Bot) draftText(ctx context.Context, c tele.Context, u *sqlite.User, d *service.Draft, text string) error {
	switch d.Kind {
	case "subject":
		if e := b.services.SaveSubject(ctx, u.ID, d.Target, text, false); e != nil {
			return e
		}
		if e := b.services.ClearDraft(ctx, u.ID); e != nil {
			return e
		}
		return b.send(c, "Предмет сохранён.", btn("Предметы", "subjects:0"))
	case "schedule":
		switch d.Step {
		case 2:
			if _, e := time.Parse("2006-01-02", text); e != nil {
				return errors.New("Введите дату ГГГГ-ММ-ДД или выберите день кнопкой.")
			}
			d.Schedule.SpecificDate = &text
			d.Schedule.DayOfWeek = nil
			d.Step = 3
		case 3:
			n, e := strconv.Atoi(text)
			if e != nil || n < 1 || n > 8 {
				return errors.New("Введите номер пары от 1 до 8.")
			}
			d.Schedule.PairNumber = n
			d.Step = 4
		case 4:
			if _, e := time.Parse("15:04", text); e != nil || len(text) != 5 {
				return errors.New("Введите время ЧЧ:ММ.")
			}
			d.Schedule.StartTime = text
			d.Step = 5
		default:
			return b.renderDraft(ctx, c, u, d)
		}
	case "absent":
		if d.Step != 2 {
			return b.renderDraft(ctx, c, u, d)
		}
		d.Comment = text
		return b.saveMark(ctx, c, u, d)
	case "stats":
		if _, e := b.services.Member(ctx, u.ID, true); e != nil {
			return e
		}
		if d.Step != 1 {
			return b.renderDraft(ctx, c, u, d)
		}
		if _, e := time.Parse("2006-01-02", text); e != nil {
			return errors.New("Введите дату ГГГГ-ММ-ДД.")
		}
		d.Date = text
		d.Step = 2
		d.Page = 0
	default:
		return errors.New("Используйте кнопки.")
	}
	if e := b.services.SaveDraft(ctx, u.ID, d); e != nil {
		return e
	}
	return b.renderDraft(ctx, c, u, d)
}
func (b *Bot) draftAction(ctx context.Context, c tele.Context, u *sqlite.User, d *service.Draft, parts []string) error {
	key := parts[0]
	if d.Kind != "absent" {
		if _, e := b.services.Member(ctx, u.ID, true); e != nil {
			return e
		}
	}
	value := func() (int64, error) {
		if len(parts) != 2 {
			return 0, errors.New("Кнопка устарела.")
		}
		return strconv.ParseInt(parts[1], 10, 64)
	}
	switch key {
	case "back":
		if d.Step > 1 {
			d.Step--
			d.Page = 0
		} else {
			if e := b.services.ClearDraft(ctx, u.ID); e != nil {
				return e
			}
			return b.menu(c, u)
		}
	case "statpage":
		n, e := value()
		if e != nil || n < 0 || n > 100000 {
			return errors.New("Некорректная страница.")
		}
		if d.Kind != "stats" && d.Kind != "schedule" {
			return errors.New("Кнопка устарела.")
		}
		d.Page = int(n)
	case "pick":
		id, e := value()
		if e != nil {
			return e
		}
		if d.Kind == "schedule" && d.Step == 1 {
			if _, e = b.services.Subject(ctx, u.ID, id); e != nil {
				return e
			}
			d.Schedule.SubjectID = id
			d.Step = 2
			d.Page = 0
		} else if d.Kind == "stats" && d.Step == 2 {
			if _, e = b.services.Subject(ctx, u.ID, id); e != nil {
				return e
			}
			d.Subject = id
			d.Step = 3
			d.Page = 0
		} else {
			return errors.New("Кнопка устарела.")
		}
	case "day":
		if d.Kind != "schedule" || d.Step != 2 {
			return errors.New("Кнопка устарела.")
		}
		day, e := value()
		if e != nil || day < 0 || day > 6 {
			return errors.New("Некорректный день.")
		}
		v := int(day)
		d.Schedule.DayOfWeek = &v
		d.Schedule.SpecificDate = nil
		d.Step = 3
	case "confirm":
		if d.Kind != "schedule" || d.Step != 5 {
			return errors.New("Кнопка устарела.")
		}
		if e := b.services.SaveSchedule(ctx, u.ID, &d.Schedule); e != nil {
			return e
		}
		if e := b.services.ClearDraft(ctx, u.ID); e != nil {
			return e
		}
		return b.send(c, "Занятие сохранено.", btn("Расписание", "schedule:0"))
	case "reason":
		if d.Kind != "absent" || d.Step != 1 || len(parts) != 2 {
			return errors.New("Кнопка устарела.")
		}
		d.Reason = parts[1]
		d.Comment = ""
		if d.Reason == "invalid" {
			return b.saveMark(ctx, c, u, d)
		}
		if d.Reason != "valid" {
			return errors.New("Выберите причину.")
		}
		d.Step = 2
	case "skip":
		if d.Kind != "absent" || d.Step != 2 {
			return errors.New("Кнопка устарела.")
		}
		d.Comment = ""
		return b.saveMark(ctx, c, u, d)
	case "stats_today":
		if d.Kind != "stats" || d.Step != 1 {
			return errors.New("Кнопка устарела.")
		}
		d.Date = b.services.Today(ctx, u.ID)
		d.Subject = 0
		d.Step = 3
		d.Page = 0
	case "stats_all":
		if d.Kind != "stats" || d.Step != 2 {
			return errors.New("Кнопка устарела.")
		}
		d.Subject = 0
		d.Step = 3
		d.Page = 0
	default:
		return errors.New("Кнопка устарела.")
	}
	if e := b.services.SaveDraft(ctx, u.ID, d); e != nil {
		return e
	}
	return b.renderDraft(ctx, c, u, d)
}
func (b *Bot) saveMark(ctx context.Context, c tele.Context, u *sqlite.User, d *service.Draft) error {
	if e := b.services.Mark(ctx, u.ID, d.Schedule.ID, d.Date, d.Reason, d.Comment); e != nil {
		return e
	}
	if e := b.services.ClearDraft(ctx, u.ID); e != nil {
		return e
	}
	return b.send(c, "Отметка принята. Пожалуйста, не переноси эту Н в бумажный журнал", btn("История", "history:0"), btn("Меню", "menu"))
}
func (b *Bot) stats(ctx context.Context, c tele.Context, u *sqlite.User, d *service.Draft) error {
	list, e := b.services.Stats(ctx, u.ID, d.Date, d.Subject)
	if e != nil {
		return e
	}
	valid := 0
	for _, a := range list {
		if a.ReasonType == "valid" {
			valid++
		}
	}
	var out strings.Builder
	fmt.Fprintf(&out, "📊 Статистика за %s\nВсего: %d · уважительных: %d · неуважительных: %d\n\n", d.Date, len(list), valid, len(list)-valid)
	type counts struct {
		name           string
		valid, invalid int
	}
	grouped := map[int64]*counts{}
	for _, a := range list {
		item := grouped[a.SubjectID]
		if item == nil {
			sub, e := b.services.Subject(ctx, u.ID, a.SubjectID)
			if e != nil {
				return e
			}
			item = &counts{name: sub.Name}
			grouped[a.SubjectID] = item
		}
		if a.ReasonType == "valid" {
			item.valid++
		} else {
			item.invalid++
		}
	}
	var ids []int64
	for id := range grouped {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return grouped[ids[i]].name < grouped[ids[j]].name })
	for _, id := range ids {
		item := grouped[id]
		fmt.Fprintf(&out, "%s: всего %d · уважительных %d · неуважительных %d\n", item.name, item.valid+item.invalid, item.valid, item.invalid)
	}
	out.WriteString("\nОтсутствующие:\n")
	start, end, e := bounds(len(list), int64(d.Page))
	if e != nil {
		return e
	}
	for _, a := range list[start:end] {
		sub, e := b.services.Subject(ctx, u.ID, a.SubjectID)
		if e != nil {
			return e
		}
		student, e := b.services.Storage.GetUserByID(ctx, a.StudentID)
		if e != nil {
			return e
		}
		fmt.Fprintf(&out, "%s · пара %d · %s %s · %s\n", sub.Name, a.PairNumber, student.FirstName, student.LastName, reason(a.ReasonType))
	}
	items := nav()
	if d.Page > 0 {
		items = append(items, btn("Предыдущая страница", fmt.Sprintf("statpage:%d", d.Page-1)))
	}
	if end < len(list) {
		items = append(items, btn("Следующая страница", fmt.Sprintf("statpage:%d", d.Page+1)))
	}
	return b.send(c, empty(out.String(), len(list)), items...)
}
