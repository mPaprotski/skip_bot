package service

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	_ "time/tzdata"

	"github.com/poseshaemost/skip-bot/internal/storage/sqlite"
)

var ErrAccess = errors.New("Недостаточно прав или доступ к группе отозван.")
var ErrDeadline = errors.New("Пара уже началась: создать, изменить или отменить отметку нельзя.")

type Services struct {
	Storage  *sqlite.Storage
	OwnerID  int64
	Timezone string
	Now      func() time.Time
}

func NewServices(s *sqlite.Storage, owner int64, zone string) *Services {
	return &Services{s, owner, zone, time.Now}
}
func (s *Services) User(ctx context.Context, telegramID int64) (*sqlite.User, error) {
	return s.Storage.GetUserByTelegramID(ctx, telegramID)
}
func (s *Services) Member(ctx context.Context, id int64, admin bool) (*sqlite.User, error) {
	u, e := s.Storage.GetUserByID(ctx, id)
	if e != nil {
		return nil, e
	}
	if !u.IsActive || u.GroupID == nil || (admin && u.Role != "admin" && (u.Role != "owner" || u.TelegramID != s.OwnerID)) {
		return nil, ErrAccess
	}
	return u, nil
}
func (s *Services) Register(ctx context.Context, id int64, username, first, last string) (*sqlite.User, error) {
	u, e := s.User(ctx, id)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return nil, e
	}
	if errors.Is(e, sql.ErrNoRows) {
		u = &sqlite.User{TelegramID: id, Username: username, FirstName: first, LastName: last, Role: "student", IsActive: true}
		if id == s.OwnerID {
			u.Role = "owner"
		}
		e = s.Storage.WithTx(ctx, func(tx *sql.Tx) error { return s.Storage.CreateUser(ctx, tx, u) })
	} else {
		if id == s.OwnerID {
			u.Role = "owner"
		} else if u.Role == "owner" {
			u.Role = "student"
		}
		u.Username = username
		u.FirstName = first
		u.LastName = last
		e = s.Storage.WithTx(ctx, func(tx *sql.Tx) error { return s.Storage.UpdateUser(ctx, tx, u) })
	}
	return u, e
}
func invite() (string, error) {
	var b [12]byte
	_, e := rand.Read(b[:])
	return hex.EncodeToString(b[:]), e
}
func (s *Services) Setup(ctx context.Context, id int64, name string) error {
	u, e := s.Storage.GetUserByID(ctx, id)
	if e != nil {
		return e
	}
	if u.TelegramID != s.OwnerID {
		return ErrAccess
	}
	name = strings.TrimSpace(name)
	if len([]rune(name)) < 1 || len([]rune(name)) > 100 {
		return errors.New("Название: от 1 до 100 символов.")
	}
	code, e := invite()
	if e != nil {
		return e
	}
	return s.Storage.WithTx(ctx, func(tx *sql.Tx) error {
		var gid int64
		e := tx.QueryRowContext(ctx, "SELECT id FROM groups ORDER BY id LIMIT 1").Scan(&gid)
		if errors.Is(e, sql.ErrNoRows) {
			r, e := tx.ExecContext(ctx, "INSERT INTO groups(name,invite_code,timezone,created_at) VALUES(?,?,?,?)", name, code, s.Timezone, s.Now().Unix())
			if e != nil {
				return e
			}
			gid, e = r.LastInsertId()
			if e != nil {
				return e
			}
		} else if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, "UPDATE users SET group_id=?,role='owner',is_active=1 WHERE id=?", gid, id)
		return e
	})
}
func (s *Services) Join(ctx context.Context, id int64, code string) error {
	u, e := s.Storage.GetUserByID(ctx, id)
	if e != nil {
		return e
	}
	if !u.IsActive {
		return ErrAccess
	}
	g, e := s.Storage.GetGroupByInviteCode(ctx, strings.TrimSpace(code))
	if e != nil {
		return errors.New("Неверный код приглашения.")
	}
	u.GroupID = &g.ID
	return s.Storage.WithTx(ctx, func(tx *sql.Tx) error { return s.Storage.UpdateUser(ctx, tx, u) })
}
func (s *Services) Group(ctx context.Context, id int64) (*sqlite.Group, error) {
	u, e := s.Member(ctx, id, false)
	if e != nil {
		return nil, e
	}
	return s.Storage.GetGroupByID(ctx, *u.GroupID)
}
func (s *Services) Invite(ctx context.Context, id int64, rotate bool) (string, error) {
	u, e := s.Member(ctx, id, true)
	if e != nil {
		return "", e
	}
	g, e := s.Storage.GetGroupByID(ctx, *u.GroupID)
	if e != nil {
		return "", e
	}
	if !rotate {
		return g.InviteCode, nil
	}
	g.InviteCode, e = invite()
	if e != nil {
		return "", e
	}
	e = s.Storage.WithTx(ctx, func(tx *sql.Tx) error {
		if e := s.Storage.UpdateGroup(ctx, tx, g); e != nil {
			return e
		}
		return s.audit(ctx, tx, id, "invite", "group", g.ID)
	})
	return g.InviteCode, e
}
func (s *Services) Members(ctx context.Context, id int64) ([]*sqlite.User, error) {
	u, e := s.Member(ctx, id, true)
	if e != nil {
		return nil, e
	}
	return s.Storage.GetUsersByGroupID(ctx, *u.GroupID)
}
func (s *Services) ManageUser(ctx context.Context, actor, target int64, action string) error {
	u, e := s.Member(ctx, actor, true)
	if e != nil {
		return e
	}
	t, e := s.Storage.GetUserByID(ctx, target)
	if e != nil {
		return e
	}
	if t.GroupID == nil || *t.GroupID != *u.GroupID || t.Role == "owner" {
		return ErrAccess
	}
	switch action {
	case "revoke":
		t.IsActive = false
	case "restore":
		t.IsActive = true
	case "admin", "student":
		if u.Role != "owner" {
			return ErrAccess
		}
		t.Role = action
	default:
		return ErrAccess
	}
	return s.Storage.WithTx(ctx, func(tx *sql.Tx) error {
		if e := s.Storage.UpdateUser(ctx, tx, t); e != nil {
			return e
		}
		if e := s.Storage.DeleteAllDialogStates(ctx, tx, target); e != nil {
			return e
		}
		return s.audit(ctx, tx, actor, action, "user", target)
	})
}
func (s *Services) audit(ctx context.Context, tx *sql.Tx, id int64, action, entity string, target int64, details ...string) error {
	var detail *string
	if len(details) > 0 {
		value := strings.Join(details, "\n")
		detail = &value
	}
	slog.Info("administrative action", "actor_id", id, "action", action, "entity", entity, "target_id", target)
	return s.Storage.CreateAdminLog(ctx, tx, &sqlite.AdminLog{AdminID: id, Action: action, EntityType: entity, EntityID: &target, Details: detail})
}
func (s *Services) Subjects(ctx context.Context, id int64) ([]*sqlite.Subject, error) {
	u, e := s.Member(ctx, id, false)
	if e != nil {
		return nil, e
	}
	return s.Storage.GetSubjectsByGroupID(ctx, *u.GroupID)
}
func (s *Services) Subject(ctx context.Context, id, target int64) (*sqlite.Subject, error) {
	u, e := s.Member(ctx, id, false)
	if e != nil {
		return nil, e
	}
	t, e := s.Storage.GetSubjectByID(ctx, target)
	if e != nil {
		return nil, e
	}
	if t.GroupID != *u.GroupID {
		return nil, ErrAccess
	}
	return t, nil
}
func (s *Services) SaveSubject(ctx context.Context, id, target int64, name string, archive bool) error {
	u, e := s.Member(ctx, id, true)
	if e != nil {
		return e
	}
	name = strings.TrimSpace(name)
	if !archive && (len([]rune(name)) < 1 || len([]rune(name)) > 100) {
		return errors.New("Название: от 1 до 100 символов.")
	}
	t := &sqlite.Subject{GroupID: *u.GroupID, Name: name}
	action := "create"
	if target != 0 {
		t, e = s.Subject(ctx, id, target)
		if e != nil {
			return e
		}
		if archive {
			t.IsArchived = true
			action = "archive"
		} else {
			t.Name = name
			action = "rename"
		}
	}
	return s.Storage.WithTx(ctx, func(tx *sql.Tx) error {
		var e error
		if target == 0 {
			e = s.Storage.CreateSubject(ctx, tx, t)
		} else {
			e = s.Storage.UpdateSubject(ctx, tx, t)
		}
		if e != nil {
			return e
		}
		return s.audit(ctx, tx, id, action, "subject", t.ID, fmt.Sprintf("name=%s archived=%t", t.Name, t.IsArchived))
	})
}
func (s *Services) Schedules(ctx context.Context, id int64, date string) ([]*sqlite.Schedule, error) {
	u, e := s.Member(ctx, id, false)
	if e != nil {
		return nil, e
	}
	if date == "" {
		return s.Storage.GetScheduleByGroupID(ctx, *u.GroupID)
	}
	if _, e = time.Parse("2006-01-02", date); e != nil {
		return nil, errors.New("Дата должна быть в формате ГГГГ-ММ-ДД.")
	}
	return s.Storage.GetScheduleForDate(ctx, *u.GroupID, date)
}
func (s *Services) Schedule(ctx context.Context, id, target int64) (*sqlite.Schedule, error) {
	u, e := s.Member(ctx, id, false)
	if e != nil {
		return nil, e
	}
	t, e := s.Storage.GetScheduleByID(ctx, target)
	if e != nil {
		return nil, e
	}
	if t.GroupID != *u.GroupID {
		return nil, ErrAccess
	}
	return t, nil
}
func (s *Services) SaveSchedule(ctx context.Context, id int64, t *sqlite.Schedule) error {
	u, e := s.Member(ctx, id, true)
	if e != nil {
		return e
	}
	sub, e := s.Subject(ctx, id, t.SubjectID)
	if e != nil {
		return e
	}
	if sub.IsArchived {
		return errors.New("Предмет архивирован.")
	}
	t.GroupID = *u.GroupID
	if t.ID != 0 {
		if _, e = s.Schedule(ctx, id, t.ID); e != nil {
			return e
		}
	}
	if (t.DayOfWeek == nil) == (t.SpecificDate == nil) || t.PairNumber < 1 || t.PairNumber > 8 {
		return errors.New("Укажите день или дату и номер пары от 1 до 8.")
	}
	if t.DayOfWeek != nil && (*t.DayOfWeek < 0 || *t.DayOfWeek > 6) {
		return errors.New("Некорректный день недели.")
	}
	if t.SpecificDate != nil {
		if _, e = time.Parse("2006-01-02", *t.SpecificDate); e != nil {
			return errors.New("Некорректная дата.")
		}
	}
	if _, e = time.Parse("15:04", t.StartTime); e != nil || len(t.StartTime) != 5 {
		return errors.New("Время должно быть в формате ЧЧ:ММ.")
	}
	return s.Storage.WithTx(ctx, func(tx *sql.Tx) error {
		action := "create"
		var e error
		if t.ID == 0 {
			e = s.Storage.CreateSchedule(ctx, tx, t)
		} else {
			action = "update"
			e = s.Storage.UpdateSchedule(ctx, tx, t)
		}
		if e != nil {
			return e
		}
		return s.audit(ctx, tx, id, action, "schedule", t.ID, s.DescribeSchedule(t))
	})
}
func (s *Services) DeleteSchedule(ctx context.Context, id, target int64) error {
	if _, e := s.Member(ctx, id, true); e != nil {
		return e
	}
	lesson, e := s.Schedule(ctx, id, target)
	if e != nil {
		return e
	}
	return s.Storage.WithTx(ctx, func(tx *sql.Tx) error {
		if e := s.Storage.DeleteSchedule(ctx, tx, target); e != nil {
			return e
		}
		return s.audit(ctx, tx, id, "delete", "schedule", target, s.DescribeSchedule(lesson))
	})
}
func (s *Services) Start(ctx context.Context, id int64, t *sqlite.Schedule, date string) (time.Time, error) {
	g, e := s.Group(ctx, id)
	if e != nil {
		return time.Time{}, e
	}
	loc, e := time.LoadLocation(g.Timezone)
	if e != nil {
		return time.Time{}, e
	}
	d, e := time.ParseInLocation("2006-01-02", date, loc)
	if e != nil {
		return time.Time{}, e
	}
	if t.SpecificDate != nil && *t.SpecificDate != date || t.DayOfWeek != nil && int(d.Weekday()) != *t.DayOfWeek {
		return time.Time{}, errors.New("На эту дату занятия нет.")
	}
	return time.ParseInLocation("2006-01-02 15:04", date+" "+t.StartTime, loc)
}

// Deadline, membership and ownership are checked inside the same transaction as the write.
func (s *Services) Mark(ctx context.Context, id, schedule int64, date, reason, comment string) error {
	if reason != "valid" && reason != "invalid" {
		return errors.New("Выберите причину.")
	}
	if len([]rune(comment)) > 500 {
		return errors.New("Комментарий: не более 500 символов.")
	}
	return s.Storage.WithTx(ctx, func(tx *sql.Tx) error {
		var group, subject int64
		var dow sql.NullInt64
		var specific sql.NullString
		var pair int
		var clock, zone string
		e := tx.QueryRowContext(ctx, `SELECT u.group_id,t.subject_id,t.day_of_week,strftime('%Y-%m-%d',t.specific_date,'unixepoch'),t.pair_number,t.start_time,g.timezone FROM users u JOIN schedule t ON t.group_id=u.group_id JOIN groups g ON g.id=u.group_id JOIN subjects sub ON sub.id=t.subject_id WHERE u.id=? AND u.is_active=1 AND t.id=? AND sub.is_archived=0`, id, schedule).Scan(&group, &subject, &dow, &specific, &pair, &clock, &zone)
		if e != nil {
			return ErrAccess
		}
		loc, e := time.LoadLocation(zone)
		if e != nil {
			return e
		}
		day, e := time.ParseInLocation("2006-01-02", date, loc)
		if e != nil {
			return errors.New("Некорректная дата.")
		}
		if specific.Valid && specific.String != date || dow.Valid && int64(day.Weekday()) != dow.Int64 {
			return errors.New("Занятия на эту дату нет.")
		}
		start, e := time.ParseInLocation("2006-01-02 15:04", date+" "+clock, loc)
		if e != nil {
			return e
		}
		if !s.Now().Before(start) {
			return ErrDeadline
		}
		stamp := sqlite.DateStamp(date)
		var previous int64
		previousErr := tx.QueryRowContext(ctx, "SELECT start_at FROM absences WHERE student_id=? AND subject_id=? AND absence_date=? AND pair_number=?", id, subject, stamp, pair).Scan(&previous)
		if previousErr == nil && !s.Now().Before(time.Unix(previous, 0)) {
			return ErrDeadline
		}
		if previousErr != nil && !errors.Is(previousErr, sql.ErrNoRows) {
			return previousErr
		}
		now := s.Now().Unix()
		_, e = tx.ExecContext(ctx, `INSERT INTO absences(student_id,subject_id,schedule_id,absence_date,pair_number,reason_type,comment,created_at,updated_at,start_at) VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(student_id,subject_id,absence_date,pair_number) DO UPDATE SET reason_type=excluded.reason_type,comment=excluded.comment,updated_at=excluded.updated_at`, id, subject, schedule, stamp, pair, reason, comment, now, now, start.Unix())
		return e
	})
}
func (s *Services) Cancel(ctx context.Context, id, target int64) error {
	return s.Storage.WithTx(ctx, func(tx *sql.Tx) error {
		var start int64
		var date, zone string
		var clock sql.NullString
		e := tx.QueryRowContext(ctx, `SELECT a.start_at,strftime('%Y-%m-%d',a.absence_date,'unixepoch'),g.timezone,t.start_time FROM absences a JOIN users u ON u.id=a.student_id JOIN subjects sub ON sub.id=a.subject_id JOIN groups g ON g.id=u.group_id LEFT JOIN schedule t ON t.id=a.schedule_id WHERE a.id=? AND a.student_id=? AND u.is_active=1 AND u.group_id=sub.group_id`, target, id).Scan(&start, &date, &zone, &clock)
		if e != nil {
			return ErrAccess
		}
		if !s.Now().Before(time.Unix(start, 0)) {
			return ErrDeadline
		}
		if clock.Valid {
			loc, e := time.LoadLocation(zone)
			if e != nil {
				return e
			}
			current, e := time.ParseInLocation("2006-01-02 15:04", date+" "+clock.String, loc)
			if e != nil {
				return e
			}
			if !s.Now().Before(current) {
				return ErrDeadline
			}
		}
		_, e = tx.ExecContext(ctx, "DELETE FROM absences WHERE id=?", target)
		return e
	})
}
func (s *Services) History(ctx context.Context, id int64, page int) ([]*sqlite.Absence, error) {
	if _, e := s.Member(ctx, id, false); e != nil {
		return nil, e
	}
	if page < 0 || page > 100000 {
		return nil, ErrAccess
	}
	return s.Storage.GetAbsencesByStudent(ctx, id, 10, page*10)
}
func (s *Services) Absence(ctx context.Context, id, target int64) (*sqlite.Absence, error) {
	if _, e := s.Member(ctx, id, false); e != nil {
		return nil, e
	}
	a, e := s.Storage.GetAbsenceByID(ctx, target)
	if e != nil {
		return nil, e
	}
	if a.StudentID != id {
		return nil, ErrAccess
	}
	return a, nil
}
func (s *Services) Stats(ctx context.Context, id int64, date string, subject int64) ([]*sqlite.Absence, error) {
	u, e := s.Member(ctx, id, true)
	if e != nil {
		return nil, e
	}
	if _, e = time.Parse("2006-01-02", date); e != nil {
		return nil, errors.New("Некорректная дата.")
	}
	if subject != 0 {
		if _, e = s.Subject(ctx, id, subject); e != nil {
			return nil, e
		}
		return s.Storage.GetAbsencesByDateAndSubject(ctx, *u.GroupID, date, subject)
	}
	return s.Storage.GetAbsencesByDate(ctx, *u.GroupID, date)
}

type Draft struct {
	Kind     string          `json:"kind"`
	Step     int             `json:"step"`
	Target   int64           `json:"target"`
	Schedule sqlite.Schedule `json:"schedule"`
	Date     string          `json:"date"`
	Reason   string          `json:"reason"`
	Comment  string          `json:"comment"`
	Subject  int64           `json:"subject"`
	Page     int             `json:"page"`
}

func (s *Services) Draft(ctx context.Context, id int64) (*Draft, error) {
	var raw string
	e := s.Storage.DB.QueryRowContext(ctx, "SELECT data FROM dialog_states WHERE user_id=?", id).Scan(&raw)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	var d Draft
	e = json.Unmarshal([]byte(raw), &d)
	return &d, e
}
func (s *Services) SaveDraft(ctx context.Context, id int64, d *Draft) error {
	if d.Kind != "setup" {
		if _, e := s.Member(ctx, id, d.Kind != "absent"); e != nil {
			return e
		}
	}
	raw, e := json.Marshal(d)
	if e != nil {
		return e
	}
	return s.Storage.WithTx(ctx, func(tx *sql.Tx) error {
		if e := s.Storage.DeleteAllDialogStates(ctx, tx, id); e != nil {
			return e
		}
		return s.Storage.SaveDialogState(ctx, tx, &sqlite.DialogState{UserID: id, DialogType: d.Kind, CurrentStep: d.Step, Data: string(raw)})
	})
}
func (s *Services) ClearDraft(ctx context.Context, id int64) error {
	return s.Storage.WithTx(ctx, func(tx *sql.Tx) error { return s.Storage.DeleteAllDialogStates(ctx, tx, id) })
}
func (s *Services) Today(ctx context.Context, id int64) string {
	g, e := s.Group(ctx, id)
	zone := s.Timezone
	if e == nil {
		zone = g.Timezone
	}
	loc, e := time.LoadLocation(zone)
	if e != nil {
		loc = time.UTC
	}
	return s.Now().In(loc).Format("2006-01-02")
}
func (s *Services) Describe(ctx context.Context, id int64, t *sqlite.Schedule) string {
	sub, e := s.Subject(ctx, id, t.SubjectID)
	name := "Предмет"
	if e == nil {
		name = sub.Name
	}
	day := ""
	if t.SpecificDate != nil {
		day = *t.SpecificDate
	} else if t.DayOfWeek != nil {
		day = []string{"Вс", "Пн", "Вт", "Ср", "Чт", "Пт", "Сб"}[*t.DayOfWeek]
	}
	return fmt.Sprintf("%s · %s · пара %d · %s", name, day, t.PairNumber, t.StartTime)
}

func (s *Services) DescribeSchedule(t *sqlite.Schedule) string {
	raw, _ := json.Marshal(t)
	return string(raw)
}
