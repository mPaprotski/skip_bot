package telegram

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/poseshaemost/skip-bot/internal/storage/sqlite"
	tele "gopkg.in/telebot.v3"
)

func TestScheduleDayDatesAndAbsenceFlow(t *testing.T) {
	b, messages := testBot(t)
	ctx := context.Background()
	u, err := b.services.Register(ctx, 1, "", "Иван", "")
	if err != nil {
		t.Fatal(err)
	}
	if err = b.services.Setup(ctx, u.ID, "Группа"); err != nil {
		t.Fatal(err)
	}
	if err = b.services.SaveSubject(ctx, u.ID, 0, "Математика", false); err != nil {
		t.Fatal(err)
	}
	subjects, err := b.services.Subjects(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	monday := 1
	weekly := &sqlite.Schedule{SubjectID: subjects[0].ID, DayOfWeek: &monday, PairNumber: 1, StartTime: "09:00"}
	date := "2026-10-12"
	oneOff := &sqlite.Schedule{SubjectID: subjects[0].ID, SpecificDate: &date, PairNumber: 2, StartTime: "10:00"}
	for _, lesson := range []*sqlite.Schedule{weekly, oneOff} {
		if err = b.services.SaveSchedule(ctx, u.ID, lesson); err != nil {
			t.Fatal(err)
		}
	}
	buttons, err := b.scheduleDayButtons(ctx, u.ID, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(buttons) != 4 || buttons[0].data != fmt.Sprintf("mark:%d:2026-10-05", weekly.ID) || buttons[1].data != fmt.Sprintf("mark:%d:2026-10-12", oneOff.ID) {
		t.Fatalf("incorrect weekly or one-off dates: %+v", buttons)
	}
	if _, err = b.scheduleDayButtons(ctx, u.ID, 7, 0); err == nil {
		t.Fatal("invalid weekday accepted")
	}
	empty, err := b.scheduleDayButtons(ctx, u.ID, 2, 0)
	if err != nil || empty[0].data != "noop" {
		t.Fatalf("empty day: %v %v", empty, err)
	}
	callback := b.api.NewContext(tele.Update{Callback: &tele.Callback{ID: "cb", Sender: &tele.User{ID: 1}, Message: &tele.Message{ID: 1, Chat: &tele.Chat{ID: 1, Type: tele.ChatPrivate}}, Data: "\faction|" + buttons[0].data}})
	if err = b.handle(callback); err != nil {
		t.Fatal(err)
	}
	draft, err := b.services.Draft(ctx, u.ID)
	if err != nil || draft == nil || draft.Kind != "absent" || draft.Date != "2026-10-05" {
		t.Fatalf("absence draft: %+v %v", draft, err)
	}
	last := (*messages)[len(*messages)-1]
	if !strings.Contains(last, "Причина отсутствия") || !strings.Contains(last, "Математика") {
		t.Fatal(last)
	}
}
