package telegram

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"
)

var scheduleWeekdays = [...]string{"Воскресенье", "Понедельник", "Вторник", "Среда", "Четверг", "Пятница", "Суббота"}

// Weekly lessons use the nearest occurrence, including today. One-off lessons
// retain their actual date so a button never marks the wrong occurrence.
func (b *Bot) scheduleDayButtons(ctx context.Context, userID, day, page int64) ([]button, error) {
	if day < 0 || day > 6 {
		return nil, errors.New("Некорректный день недели.")
	}
	list, err := b.services.Schedules(ctx, userID, "")
	if err != nil {
		return nil, err
	}
	today, err := time.Parse("2006-01-02", b.services.Today(ctx, userID))
	if err != nil {
		return nil, err
	}
	next := today.AddDate(0, 0, (int(day)-int(today.Weekday())+7)%7)
	type lesson struct {
		date   string
		pair   int
		start  string
		button button
	}
	var lessons []lesson
	for _, entry := range list {
		date := next
		if entry.SpecificDate != nil {
			date, err = time.Parse("2006-01-02", *entry.SpecificDate)
			if err != nil {
				return nil, err
			}
			if date.Before(today) || int64(date.Weekday()) != day {
				continue
			}
		} else if entry.DayOfWeek == nil || int64(*entry.DayOfWeek) != day {
			continue
		}
		subject, err := b.services.Subject(ctx, userID, entry.SubjectID)
		if err != nil {
			return nil, err
		}
		if subject.IsArchived {
			continue
		}
		dateKey := date.Format("2006-01-02")
		label := fmt.Sprintf("%s · %s · пара %d · %s", date.Format("02.01"), entry.StartTime, entry.PairNumber, subject.Name)
		lessons = append(lessons, lesson{dateKey, entry.PairNumber, entry.StartTime, btn(label, fmt.Sprintf("mark:%d:%s", entry.ID, dateKey))})
	}
	sort.SliceStable(lessons, func(i, j int) bool {
		if lessons[i].date != lessons[j].date {
			return lessons[i].date < lessons[j].date
		}
		if lessons[i].start != lessons[j].start {
			return lessons[i].start < lessons[j].start
		}
		return lessons[i].pair < lessons[j].pair
	})
	start, end, err := bounds(len(lessons), page)
	if err != nil {
		return nil, err
	}
	items := []button{}
	for _, entry := range lessons[start:end] {
		items = append(items, entry.button)
	}
	if len(lessons) == 0 {
		items = append(items, btn("Занятий нет", "noop"))
	}
	if page > 0 {
		items = append(items, btn("◀️", fmt.Sprintf("schedule_day:%d:%d", day, page-1)))
	}
	if end < len(lessons) {
		items = append(items, btn("▶️", fmt.Sprintf("schedule_day:%d:%d", day, page+1)))
	}
	items = append(items, btn("Дни недели", "view:0"), btn("Главное меню", "menu"))
	return items, nil
}
