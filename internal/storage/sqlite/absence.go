package sqlite

import (
	"context"
	"database/sql"
	"time"
)

// Absence представляет отметку отсутствия
type Absence struct {
	ID          int64
	StudentID   int64
	SubjectID   int64
	ScheduleID  *int64
	AbsenceDate string // формат YYYY-MM-DD
	PairNumber  int
	ReasonType  string // valid, invalid
	Comment     *string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// CreateAbsence создаёт новую отметку
func (s *Storage) CreateAbsence(ctx context.Context, tx *sql.Tx, absence *Absence) error {
	now := time.Now().Unix()
	result, err := tx.ExecContext(ctx, `
		INSERT INTO absences (student_id, subject_id, schedule_id, absence_date, pair_number, reason_type, comment, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, absence.StudentID, absence.SubjectID, absence.ScheduleID, DateStamp(absence.AbsenceDate),
		absence.PairNumber, absence.ReasonType, absence.Comment, now, now)
	if err != nil {
		return err
	}
	absence.ID, _ = result.LastInsertId()
	return nil
}

// GetAbsenceByID находит отметку по ID
func (s *Storage) GetAbsenceByID(ctx context.Context, id int64) (*Absence, error) {
	row := s.DB.QueryRowContext(ctx, `
		SELECT id, student_id, subject_id, schedule_id, strftime('%Y-%m-%d', absence_date, 'unixepoch'), pair_number, reason_type, comment, created_at, updated_at
		FROM absences WHERE id = ?
	`, id)
	return scanAbsence(row)
}

// GetAbsenceByStudentAndSchedule находит отметку студента на конкретное занятие
func (s *Storage) GetAbsenceByStudentAndSchedule(ctx context.Context, studentID, subjectID int64, date string, pairNumber int) (*Absence, error) {
	row := s.DB.QueryRowContext(ctx, `
		SELECT id, student_id, subject_id, schedule_id, strftime('%Y-%m-%d', absence_date, 'unixepoch'), pair_number, reason_type, comment, created_at, updated_at
		FROM absences 
		WHERE student_id = ? AND subject_id = ? AND absence_date = ? AND pair_number = ?
	`, studentID, subjectID, DateStamp(date), pairNumber)
	return scanAbsence(row)
}

// GetAbsencesByStudent получает все отметки студента
func (s *Storage) GetAbsencesByStudent(ctx context.Context, studentID int64, limit, offset int) ([]*Absence, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, student_id, subject_id, schedule_id, strftime('%Y-%m-%d', absence_date, 'unixepoch'), pair_number, reason_type, comment, created_at, updated_at
		FROM absences WHERE student_id = ?
		ORDER BY absence_date DESC, pair_number DESC
		LIMIT ? OFFSET ?
	`, studentID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var absences []*Absence
	for rows.Next() {
		absence, err := scanAbsence(rows)
		if err != nil {
			return nil, err
		}
		absences = append(absences, absence)
	}
	return absences, rows.Err()
}

// GetAbsencesByDate получает все отметки на конкретную дату
func (s *Storage) GetAbsencesByDate(ctx context.Context, groupID int64, date string) ([]*Absence, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT a.id, a.student_id, a.subject_id, a.schedule_id, strftime('%Y-%m-%d', a.absence_date, 'unixepoch'), a.pair_number, a.reason_type, a.comment, a.created_at, a.updated_at
		FROM absences a
		JOIN users u ON a.student_id = u.id
		WHERE u.group_id = ? AND a.absence_date = ?
		ORDER BY a.pair_number
	`, groupID, DateStamp(date))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var absences []*Absence
	for rows.Next() {
		absence, err := scanAbsence(rows)
		if err != nil {
			return nil, err
		}
		absences = append(absences, absence)
	}
	return absences, rows.Err()
}

// GetAbsencesByDateAndSubject получает отметки на конкретную дату и предмет
func (s *Storage) GetAbsencesByDateAndSubject(ctx context.Context, groupID int64, date string, subjectID int64) ([]*Absence, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT a.id, a.student_id, a.subject_id, a.schedule_id, strftime('%Y-%m-%d', a.absence_date, 'unixepoch'), a.pair_number, a.reason_type, a.comment, a.created_at, a.updated_at
		FROM absences a
		JOIN users u ON a.student_id = u.id
		WHERE u.group_id = ? AND a.absence_date = ? AND a.subject_id = ?
		ORDER BY a.pair_number
	`, groupID, DateStamp(date), subjectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var absences []*Absence
	for rows.Next() {
		absence, err := scanAbsence(rows)
		if err != nil {
			return nil, err
		}
		absences = append(absences, absence)
	}
	return absences, rows.Err()
}

// UpdateAbsence обновляет отметку
func (s *Storage) UpdateAbsence(ctx context.Context, tx *sql.Tx, absence *Absence) error {
	now := time.Now().Unix()
	_, err := tx.ExecContext(ctx, `
		UPDATE absences SET reason_type = ?, comment = ?, updated_at = ?
		WHERE id = ?
	`, absence.ReasonType, absence.Comment, now, absence.ID)
	return err
}

// DeleteAbsence удаляет отметку
func (s *Storage) DeleteAbsence(ctx context.Context, tx *sql.Tx, absenceID int64) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM absences WHERE id = ?`, absenceID)
	return err
}

// CountAbsencesByStudent подсчитывает количество отметок студента
func (s *Storage) CountAbsencesByStudent(ctx context.Context, studentID int64) (int, error) {
	var count int
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM absences WHERE student_id = ?`, studentID).Scan(&count)
	return count, err
}

func scanAbsence(row interface{ Scan(...interface{}) error }) (*Absence, error) {
	var absence Absence
	var scheduleID sql.NullInt64
	var comment sql.NullString
	var createdAt, updatedAt int64

	err := row.Scan(&absence.ID, &absence.StudentID, &absence.SubjectID, &scheduleID,
		&absence.AbsenceDate, &absence.PairNumber, &absence.ReasonType, &comment, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}

	if scheduleID.Valid {
		absence.ScheduleID = &scheduleID.Int64
	}
	if comment.Valid {
		absence.Comment = &comment.String
	}
	absence.CreatedAt = time.Unix(createdAt, 0).UTC()
	absence.UpdatedAt = time.Unix(updatedAt, 0).UTC()

	return &absence, nil
}
