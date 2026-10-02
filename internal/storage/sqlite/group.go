package sqlite

import (
	"context"
	"database/sql"
	"time"
)

// Group представляет учебную группу
type Group struct {
	ID         int64
	Name       string
	InviteCode string
	Timezone   string
	CreatedAt  time.Time
}

// CreateGroup создаёт новую группу
func (s *Storage) CreateGroup(ctx context.Context, tx *sql.Tx, group *Group) error {
	now := time.Now().Unix()
	result, err := tx.ExecContext(ctx, `
		INSERT INTO groups (name, invite_code, timezone, created_at)
		VALUES (?, ?, ?, ?)
	`, group.Name, group.InviteCode, group.Timezone, now)
	if err != nil {
		return err
	}
	group.ID, _ = result.LastInsertId()
	return nil
}

// GetGroupByInviteCode находит группу по коду приглашения
func (s *Storage) GetGroupByInviteCode(ctx context.Context, code string) (*Group, error) {
	row := s.DB.QueryRowContext(ctx, `
		SELECT id, name, invite_code, timezone, created_at
		FROM groups WHERE invite_code = ?
	`, code)
	return scanGroup(row)
}

// GetGroupByID находит группу по ID
func (s *Storage) GetGroupByID(ctx context.Context, id int64) (*Group, error) {
	row := s.DB.QueryRowContext(ctx, `
		SELECT id, name, invite_code, timezone, created_at
		FROM groups WHERE id = ?
	`, id)
	return scanGroup(row)
}

// UpdateGroup обновляет группу
func (s *Storage) UpdateGroup(ctx context.Context, tx *sql.Tx, group *Group) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE groups SET name = ?, invite_code = ?, timezone = ?
		WHERE id = ?
	`, group.Name, group.InviteCode, group.Timezone, group.ID)
	return err
}

// GetAllGroups получает все группы
func (s *Storage) GetAllGroups(ctx context.Context) ([]*Group, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, name, invite_code, timezone, created_at FROM groups`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var groups []*Group
	for rows.Next() {
		group, err := scanGroup(rows)
		if err != nil {
			return nil, err
		}
		groups = append(groups, group)
	}
	return groups, rows.Err()
}

func scanGroup(row interface{ Scan(...interface{}) error }) (*Group, error) {
	var group Group
	var createdAt int64
	err := row.Scan(&group.ID, &group.Name, &group.InviteCode, &group.Timezone, &createdAt)
	if err != nil {
		return nil, err
	}
	group.CreatedAt = time.Unix(createdAt, 0).UTC()
	return &group, nil
}
