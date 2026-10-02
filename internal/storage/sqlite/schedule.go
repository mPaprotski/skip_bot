package sqlite

import (
	"context"
	"database/sql"
	"time"
)

// Schedule представляет занятие в расписании
type Schedule struct {
	ID           int64
	GroupID      int64
	SubjectID    int64
	DayOfWeek    *int    // 0=воскресенье, 1=понедельник, ..., NULL если конкретная дата
	SpecificDate *string // формат YYYY-MM-DD, NULL если регулярная пара
	PairNumber   int
	StartTime    string // формат HH:MM
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// CreateSchedule создаёт новое занятие
func (s *Storage) CreateSchedule(ctx context.Context, tx *sql.Tx, schedule *Schedule) error {
	now := time.Now().Unix()
	result, err := tx.ExecContext(ctx, `
		INSERT INTO schedule (group_id, subject_id, day_of_week, specific_date, pair_number, start_time, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, schedule.GroupID, schedule.SubjectID, schedule.DayOfWeek, OptionalDateStamp(schedule.SpecificDate),
		schedule.PairNumber, schedule.StartTime, now, now)
	if err != nil {
		return err
	}
	schedule.ID, _ = result.LastInsertId()
	return nil
}

// GetScheduleByID находит занятие по ID
func (s *Storage) GetScheduleByID(ctx context.Context, id int64) (*Schedule, error) {
	row := s.DB.QueryRowContext(ctx, `
		SELECT id, group_id, subject_id, day_of_week, strftime('%Y-%m-%d', specific_date, 'unixepoch'), pair_number, start_time, created_at, updated_at
		FROM schedule WHERE id = ?
	`, id)
	return scanSchedule(row)
}

// GetScheduleByGroupID получает расписание группы
func (s *Storage) GetScheduleByGroupID(ctx context.Context, groupID int64) ([]*Schedule, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, group_id, subject_id, day_of_week, strftime('%Y-%m-%d', specific_date, 'unixepoch'), pair_number, start_time, created_at, updated_at
		FROM schedule WHERE group_id = ?
		ORDER BY day_of_week, pair_number
	`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var schedules []*Schedule
	for rows.Next() {
		schedule, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		schedules = append(schedules, schedule)
	}
	return schedules, rows.Err()
}

// GetScheduleForDate получает расписание на конкретную дату
func (s *Storage) GetScheduleForDate(ctx context.Context, groupID int64, date string) ([]*Schedule, error) {
	// Получаем день недели из даты
	var dayOfWeek int
	err := s.DB.QueryRowContext(ctx, `SELECT CAST(strftime('%w', ?) AS INTEGER)`, date).Scan(&dayOfWeek)
	if err != nil {
		return nil, err
	}

	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, group_id, subject_id, day_of_week, strftime('%Y-%m-%d', specific_date, 'unixepoch'), pair_number, start_time, created_at, updated_at
		FROM schedule 
		WHERE group_id = ? AND (specific_date = ? OR (specific_date IS NULL AND day_of_week = ?))
		ORDER BY pair_number
	`, groupID, DateStamp(date), dayOfWeek)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var schedules []*Schedule
	for rows.Next() {
		schedule, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		schedules = append(schedules, schedule)
	}
	return schedules, rows.Err()
}

// UpdateSchedule обновляет занятие
func (s *Storage) UpdateSchedule(ctx context.Context, tx *sql.Tx, schedule *Schedule) error {
	now := time.Now().Unix()
	_, err := tx.ExecContext(ctx, `
		UPDATE schedule SET subject_id = ?, day_of_week = ?, specific_date = ?, pair_number = ?, start_time = ?, updated_at = ?
		WHERE id = ?
	`, schedule.SubjectID, schedule.DayOfWeek, OptionalDateStamp(schedule.SpecificDate), schedule.PairNumber, schedule.StartTime, now, schedule.ID)
	return err
}

// DeleteSchedule удаляет занятие
func (s *Storage) DeleteSchedule(ctx context.Context, tx *sql.Tx, scheduleID int64) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM schedule WHERE id = ?`, scheduleID)
	return err
}

func scanSchedule(row interface{ Scan(...interface{}) error }) (*Schedule, error) {
	var schedule Schedule
	var dayOfWeek sql.NullInt64
	var specificDate sql.NullString
	var createdAt, updatedAt int64

	err := row.Scan(&schedule.ID, &schedule.GroupID, &schedule.SubjectID, &dayOfWeek, &specificDate,
		&schedule.PairNumber, &schedule.StartTime, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}

	if dayOfWeek.Valid {
		d := int(dayOfWeek.Int64)
		schedule.DayOfWeek = &d
	}
	if specificDate.Valid {
		schedule.SpecificDate = &specificDate.String
	}
	schedule.CreatedAt = time.Unix(createdAt, 0).UTC()
	schedule.UpdatedAt = time.Unix(updatedAt, 0).UTC()

	return &schedule, nil
}
