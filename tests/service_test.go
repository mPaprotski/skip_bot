package tests

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/poseshaemost/skip-bot/internal/service"
	"github.com/poseshaemost/skip-bot/internal/storage/sqlite"
	"github.com/poseshaemost/skip-bot/migrations"
	"github.com/poseshaemost/skip-bot/pkg/migrate"
)

var ctx = context.Background()

type fixture struct {
	s                     *service.Services
	store                 *sqlite.Storage
	owner, student, other *sqlite.User
	subject               int64
	lesson                *sqlite.Schedule
	now                   time.Time
	path                  string
}

func must(t *testing.T, e error) {
	t.Helper()
	if e != nil {
		t.Fatal(e)
	}
}
func open(t *testing.T, path string) *sqlite.Storage {
	t.Helper()
	store, e := sqlite.New(path)
	must(t, e)
	list, e := migrate.LoadMigrationsFromFS(migrations.Files, ".")
	must(t, e)
	must(t, migrate.NewMigrator(store.DB, list).Migrate(context.Background()))
	return store
}
func setup(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{path: filepath.Join(t.TempDir(), "test.db"), now: time.Date(2026, 10, 2, 5, 59, 59, 0, time.UTC)}
	f.store = open(t, f.path)
	t.Cleanup(func() { _ = f.store.Close() })
	f.s = service.NewServices(f.store, 1, "Europe/Minsk")
	f.s.Now = func() time.Time { return f.now }
	var e error
	f.owner, e = f.s.Register(ctx, 1, "owner", "Староста", "")
	must(t, e)
	must(t, f.s.Setup(ctx, f.owner.ID, "Группа"))
	f.owner, e = f.s.User(ctx, 1)
	must(t, e)
	code, e := f.s.Invite(ctx, f.owner.ID, false)
	must(t, e)
	f.student, e = f.s.Register(ctx, 2, "student", "Иван", "Иванов")
	must(t, e)
	must(t, f.s.Join(ctx, f.student.ID, code))
	f.other, e = f.s.Register(ctx, 3, "other", "Пётр", "")
	must(t, e)
	must(t, f.s.Join(ctx, f.other.ID, code))
	must(t, f.s.SaveSubject(ctx, f.owner.ID, 0, "Математика", false))
	subjects, e := f.s.Subjects(ctx, f.owner.ID)
	must(t, e)
	f.subject = subjects[0].ID
	day := 5
	f.lesson = &sqlite.Schedule{SubjectID: f.subject, DayOfWeek: &day, PairNumber: 1, StartTime: "09:00"}
	must(t, f.s.SaveSchedule(ctx, f.owner.ID, f.lesson))
	return f
}
func TestAbsenceLifecycleAndDeadline(t *testing.T) {
	f := setup(t)
	date := "2026-10-02"
	must(t, f.s.Mark(ctx, f.student.ID, f.lesson.ID, date, "valid", "справка"))
	must(t, f.s.Mark(ctx, f.student.ID, f.lesson.ID, date, "invalid", ""))
	list, e := f.s.History(ctx, f.student.ID, 0)
	must(t, e)
	if len(list) != 1 || list[0].ReasonType != "invalid" {
		t.Fatalf("expected unique updated absence: %+v", list)
	}
	if !errors.Is(f.s.Cancel(ctx, f.other.ID, list[0].ID), service.ErrAccess) {
		t.Fatal("another student cancelled absence")
	}
	must(t, f.s.Cancel(ctx, f.student.ID, list[0].ID))
	must(t, f.s.Mark(ctx, f.student.ID, f.lesson.ID, date, "valid", ""))
	list, e = f.s.History(ctx, f.student.ID, 0)
	must(t, e)
	f.now = time.Date(2026, 10, 2, 6, 0, 0, 0, time.UTC)
	if !errors.Is(f.s.Mark(ctx, f.student.ID, f.lesson.ID, date, "invalid", ""), service.ErrDeadline) {
		t.Fatal("write at exact deadline succeeded")
	}
	if !errors.Is(f.s.Cancel(ctx, f.student.ID, list[0].ID), service.ErrDeadline) {
		t.Fatal("cancel at deadline succeeded")
	}
	f.now = f.now.Add(time.Hour)
	if !errors.Is(f.s.Mark(ctx, f.other.ID, f.lesson.ID, date, "valid", ""), service.ErrDeadline) {
		t.Fatal("late creation succeeded")
	}
	var kind string
	must(t, f.store.DB.QueryRow("SELECT typeof(absence_date) FROM absences").Scan(&kind))
	if kind != "integer" {
		t.Fatal("date is not integer")
	}
}
func TestPermissionsAndRevocation(t *testing.T) {
	f := setup(t)
	if !errors.Is(f.s.SaveSubject(ctx, f.student.ID, 0, "X", false), service.ErrAccess) {
		t.Fatal("student created subject")
	}
	if !errors.Is(f.s.SaveSchedule(ctx, f.student.ID, f.lesson), service.ErrAccess) {
		t.Fatal("student edited schedule")
	}
	if _, e := f.s.Stats(ctx, f.student.ID, "2026-10-02", 0); !errors.Is(e, service.ErrAccess) {
		t.Fatal("student read stats")
	}
	must(t, f.s.ManageUser(ctx, f.owner.ID, f.other.ID, "admin"))
	must(t, f.s.SaveSubject(ctx, f.other.ID, 0, "Физика", false))
	if !errors.Is(f.s.ManageUser(ctx, f.other.ID, f.student.ID, "admin"), service.ErrAccess) {
		t.Fatal("admin assigned role")
	}
	if !errors.Is(f.s.ManageUser(ctx, f.owner.ID, f.owner.ID, "revoke"), service.ErrAccess) {
		t.Fatal("owner revoked")
	}
	must(t, f.s.ManageUser(ctx, f.other.ID, f.student.ID, "revoke"))
	code, e := f.s.Invite(ctx, f.owner.ID, false)
	must(t, e)
	if !errors.Is(f.s.Join(ctx, f.student.ID, code), service.ErrAccess) {
		t.Fatal("revoked user rejoined")
	}
	if !errors.Is(f.s.Mark(ctx, f.student.ID, f.lesson.ID, "2026-10-02", "valid", ""), service.ErrAccess) {
		t.Fatal("revoked user marked")
	}
	if _, e := f.s.History(ctx, f.student.ID, 0); !errors.Is(e, service.ErrAccess) {
		t.Fatal("revoked user accessed history")
	}
	must(t, f.s.ManageUser(ctx, f.owner.ID, f.student.ID, "restore"))
	must(t, f.s.Mark(ctx, f.student.ID, f.lesson.ID, "2026-10-02", "valid", ""))
	must(t, f.s.SaveSubject(ctx, f.owner.ID, f.subject, "", true))
	if e = f.s.Mark(ctx, f.other.ID, f.lesson.ID, "2026-10-02", "valid", ""); e == nil {
		t.Fatal("archived subject accepted")
	}
}
func TestScheduleDatesIsolationAndStats(t *testing.T) {
	f := setup(t)
	date := "2026-10-03"
	t2 := &sqlite.Schedule{SubjectID: f.subject, SpecificDate: &date, PairNumber: 2, StartTime: "12:00"}
	must(t, f.s.SaveSchedule(ctx, f.owner.ID, t2))
	list, e := f.s.Schedules(ctx, f.student.ID, date)
	must(t, e)
	if len(list) != 1 || list[0].SpecificDate == nil || *list[0].SpecificDate != date {
		t.Fatal("one-off schedule missing")
	}
	if e = f.s.Mark(ctx, f.student.ID, t2.ID, "2026-10-02", "valid", ""); e == nil {
		t.Fatal("wrong date accepted")
	}
	must(t, f.s.Mark(ctx, f.student.ID, t2.ID, date, "valid", ""))
	must(t, f.s.Mark(ctx, f.other.ID, t2.ID, date, "invalid", ""))
	stats, e := f.s.Stats(ctx, f.owner.ID, date, f.subject)
	must(t, e)
	if len(stats) != 2 {
		t.Fatal("stats incomplete")
	}
	duplicate := *t2
	duplicate.ID = 0
	if e = f.s.SaveSchedule(ctx, f.owner.ID, &duplicate); e == nil {
		t.Fatal("duplicate one-off schedule accepted")
	}
	bad := *t2
	bad.PairNumber = 0
	if e = f.s.SaveSchedule(ctx, f.owner.ID, &bad); e == nil {
		t.Fatal("invalid pair accepted")
	}
	// Foreign group objects cannot be edited or marked even by an administrator.
	var gid int64
	must(t, f.store.WithTx(ctx, func(tx *sql.Tx) error {
		r, e := tx.ExecContext(ctx, "INSERT INTO groups(name,invite_code,timezone,created_at) VALUES('foreign','foreign','Europe/Minsk',0)")
		if e != nil {
			return e
		}
		gid, e = r.LastInsertId()
		return e
	}))
	var foreign int64
	must(t, f.store.DB.QueryRow("INSERT INTO subjects(group_id,name,created_at,updated_at) VALUES(?,'foreign',0,0) RETURNING id", gid).Scan(&foreign))
	if !errors.Is(f.s.SaveSubject(ctx, f.owner.ID, foreign, "hack", false), service.ErrAccess) {
		t.Fatal("foreign subject changed")
	}
	must(t, f.s.DeleteSchedule(ctx, f.owner.ID, t2.ID))
	list2, e := f.s.History(ctx, f.student.ID, 0)
	must(t, e)
	if len(list2) != 1 || list2[0].ScheduleID != nil {
		t.Fatal("delete lost absence history")
	}
}
func TestRestartDraftAndStorage(t *testing.T) {
	f := setup(t)
	must(t, f.s.SaveDraft(ctx, f.owner.ID, &service.Draft{Kind: "schedule", Step: 4, Schedule: *f.lesson}))
	must(t, f.store.Close())
	f.store = open(t, f.path)
	f.s = service.NewServices(f.store, 1, "Europe/Minsk")
	d, e := f.s.Draft(ctx, f.owner.ID)
	must(t, e)
	if d == nil || d.Step != 4 || d.Schedule.SubjectID != f.subject {
		t.Fatal("draft lost after restart")
	}
	must(t, f.s.SaveDraft(ctx, f.owner.ID, &service.Draft{Kind: "subject"}))
	var count int
	must(t, f.store.DB.QueryRow("SELECT count(*) FROM dialog_states WHERE user_id=?", f.owner.ID).Scan(&count))
	if count != 1 {
		t.Fatal("multiple active drafts")
	}
	u, e := f.s.Register(ctx, 1, "changed", "Староста", "")
	must(t, e)
	if u.ID != f.owner.ID {
		t.Fatal("duplicate registration")
	}
	var fk int
	var wal string
	must(t, f.store.DB.QueryRow("PRAGMA foreign_keys").Scan(&fk))
	must(t, f.store.DB.QueryRow("PRAGMA journal_mode").Scan(&wal))
	if fk != 1 || wal != "wal" {
		t.Fatal("SQLite pragmas missing")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, e = f.s.Subjects(cancelled, f.owner.ID); e == nil {
		t.Fatal("cancelled context ignored")
	}
}

func TestScheduleChangeCannotBypassDeadlineAndInviteRotation(t *testing.T) {
	f := setup(t)
	must(t, f.s.Mark(ctx, f.student.ID, f.lesson.ID, "2026-10-02", "valid", ""))
	list, e := f.s.History(ctx, f.student.ID, 0)
	must(t, e)
	f.lesson.StartTime = "08:00"
	must(t, f.s.SaveSchedule(ctx, f.owner.ID, f.lesson))
	if !errors.Is(f.s.Cancel(ctx, f.student.ID, list[0].ID), service.ErrDeadline) {
		t.Fatal("earlier start allowed cancellation")
	}
	f.lesson.StartTime = "11:00"
	must(t, f.s.SaveSchedule(ctx, f.owner.ID, f.lesson))
	f.now = time.Date(2026, 10, 2, 6, 0, 0, 0, time.UTC)
	if !errors.Is(f.s.Mark(ctx, f.student.ID, f.lesson.ID, "2026-10-02", "invalid", ""), service.ErrDeadline) {
		t.Fatal("moving schedule unlocked original deadline")
	}
	old, e := f.s.Invite(ctx, f.owner.ID, false)
	must(t, e)
	newCode, e := f.s.Invite(ctx, f.owner.ID, true)
	must(t, e)
	if old == newCode {
		t.Fatal("invite did not rotate")
	}
	u, e := f.s.Register(ctx, 10, "", "Новый", "")
	must(t, e)
	if e = f.s.Join(ctx, u.ID, old); e == nil {
		t.Fatal("old invite accepted")
	}
	must(t, f.s.Join(ctx, u.ID, newCode))
	if _, e = f.s.Group(ctx, f.student.ID); e != nil {
		t.Fatal("rotation disconnected student")
	}
}
func TestMigrationPreservesLegacyData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	store, e := sqlite.New(path)
	must(t, e)
	defer store.Close()
	list, e := migrate.LoadMigrationsFromFS(migrations.Files, ".")
	must(t, e)
	must(t, migrate.NewMigrator(store.DB, list[:2]).Migrate(ctx))
	_, e = store.DB.Exec(`INSERT INTO groups VALUES(1,'Group','invite','Europe/Minsk',0); INSERT INTO users VALUES(1,1,'','Owner','','owner',1,1,0,0); INSERT INTO subjects VALUES(1,1,'Math',0,0,0); INSERT INTO schedule VALUES(1,1,1,NULL,'2026-10-02',1,'09:00',0,0); INSERT INTO absences VALUES(1,1,1,1,'2026-10-02',1,'valid','note',0,0);`)
	must(t, e)
	must(t, migrate.NewMigrator(store.DB, list).Migrate(ctx))
	a, e := store.GetAbsenceByID(ctx, 1)
	must(t, e)
	if a.AbsenceDate != "2026-10-02" || a.Comment == nil || *a.Comment != "note" {
		t.Fatal("legacy history lost")
	}
	lesson, e := store.GetScheduleByID(ctx, 1)
	must(t, e)
	if lesson.SpecificDate == nil || *lesson.SpecificDate != "2026-10-02" {
		t.Fatal("legacy schedule lost")
	}
	var fk int
	must(t, store.DB.QueryRow("SELECT count(*) FROM pragma_foreign_key_check").Scan(&fk))
	if fk != 0 {
		t.Fatal("migration broke foreign keys")
	}
	must(t, migrate.NewMigrator(store.DB, list).Rollback(ctx))
	must(t, migrate.NewMigrator(store.DB, list).Migrate(ctx))
}
