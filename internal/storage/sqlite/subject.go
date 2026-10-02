package sqlite

import (
	"context"
	"database/sql"
	"time"
)

// Subject представляет предмет
type Subject struct {
	ID         int64
	GroupID    int64
	Name       string
	IsArchived bool
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// CreateSubject создаёт новый предмет
func (s *Storage) CreateSubject(ctx context.Context, tx *sql.Tx, subject *Subject) error {
	now := time.Now().Unix()
	result, err := tx.ExecContext(ctx, `
		INSERT INTO subjects (group_id, name, is_archived, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
	`, subject.GroupID, subject.Name, subject.IsArchived, now, now)
	if err != nil {
		return err
	}
	subject.ID, _ = result.LastInsertId()
	return nil
}

// GetSubjectByID находит предмет по ID
func (s *Storage) GetSubjectByID(ctx context.Context, id int64) (*Subject, error) {
	row := s.DB.QueryRowContext(ctx, `
		SELECT id, group_id, name, is_archived, created_at, updated_at
		FROM subjects WHERE id = ?
	`, id)
	return scanSubject(row)
}

// GetSubjectsByGroupID получает все предметы группы
func (s *Storage) GetSubjectsByGroupID(ctx context.Context, groupID int64) ([]*Subject, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, group_id, name, is_archived, created_at, updated_at
		FROM subjects WHERE group_id = ? AND is_archived = 0
		ORDER BY name
	`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var subjects []*Subject
	for rows.Next() {
		subject, err := scanSubject(rows)
		if err != nil {
			return nil, err
		}
		subjects = append(subjects, subject)
	}
	return subjects, rows.Err()
}

// GetAllSubjectsByGroupID получает все предметы группы, включая архивные
func (s *Storage) GetAllSubjectsByGroupID(ctx context.Context, groupID int64) ([]*Subject, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, group_id, name, is_archived, created_at, updated_at
		FROM subjects WHERE group_id = ?
		ORDER BY name
	`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var subjects []*Subject
	for rows.Next() {
		subject, err := scanSubject(rows)
		if err != nil {
			return nil, err
		}
		subjects = append(subjects, subject)
	}
	return subjects, rows.Err()
}

// UpdateSubject обновляет предмет
func (s *Storage) UpdateSubject(ctx context.Context, tx *sql.Tx, subject *Subject) error {
	now := time.Now().Unix()
	_, err := tx.ExecContext(ctx, `
		UPDATE subjects SET name = ?, is_archived = ?, updated_at = ?
		WHERE id = ?
	`, subject.Name, subject.IsArchived, now, subject.ID)
	return err
}

// ArchiveSubject архивирует предмет
func (s *Storage) ArchiveSubject(ctx context.Context, tx *sql.Tx, subjectID int64) error {
	now := time.Now().Unix()
	_, err := tx.ExecContext(ctx, `UPDATE subjects SET is_archived = 1, updated_at = ? WHERE id = ?`, now, subjectID)
	return err
}

func scanSubject(row interface{ Scan(...interface{}) error }) (*Subject, error) {
	var subject Subject
	var createdAt, updatedAt int64
	err := row.Scan(&subject.ID, &subject.GroupID, &subject.Name, &subject.IsArchived, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	subject.CreatedAt = time.Unix(createdAt, 0).UTC()
	subject.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return &subject, nil
}
