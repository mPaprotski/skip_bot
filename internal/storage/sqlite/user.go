package sqlite

import (
	"context"
	"database/sql"
	"time"
)

// User представляет пользователя
type User struct {
	ID         int64
	TelegramID int64
	Username   string
	FirstName  string
	LastName   string
	Role       string // student, admin, owner
	GroupID    *int64
	IsActive   bool
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// CreateUser создаёт нового пользователя
func (s *Storage) CreateUser(ctx context.Context, tx *sql.Tx, user *User) error {
	now := time.Now().Unix()
	result, err := tx.ExecContext(ctx, `
		INSERT INTO users (telegram_id, username, first_name, last_name, role, group_id, is_active, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, user.TelegramID, user.Username, user.FirstName, user.LastName, user.Role, user.GroupID, user.IsActive, now, now)
	if err != nil {
		return err
	}
	user.ID, _ = result.LastInsertId()
	return nil
}

// GetUserByTelegramID находит пользователя по Telegram ID
func (s *Storage) GetUserByTelegramID(ctx context.Context, telegramID int64) (*User, error) {
	row := s.DB.QueryRowContext(ctx, `
		SELECT id, telegram_id, username, first_name, last_name, role, group_id, is_active, created_at, updated_at
		FROM users WHERE telegram_id = ?
	`, telegramID)
	return scanUser(row)
}

// GetUserByID находит пользователя по ID
func (s *Storage) GetUserByID(ctx context.Context, id int64) (*User, error) {
	row := s.DB.QueryRowContext(ctx, `
		SELECT id, telegram_id, username, first_name, last_name, role, group_id, is_active, created_at, updated_at
		FROM users WHERE id = ?
	`, id)
	return scanUser(row)
}

// UpdateUser обновляет пользователя
func (s *Storage) UpdateUser(ctx context.Context, tx *sql.Tx, user *User) error {
	now := time.Now().Unix()
	_, err := tx.ExecContext(ctx, `
		UPDATE users SET username = ?, first_name = ?, last_name = ?, role = ?, group_id = ?, is_active = ?, updated_at = ?
		WHERE id = ?
	`, user.Username, user.FirstName, user.LastName, user.Role, user.GroupID, user.IsActive, now, user.ID)
	return err
}

// GetUsersByGroupID получает всех пользователей группы
func (s *Storage) GetUsersByGroupID(ctx context.Context, groupID int64) ([]*User, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, telegram_id, username, first_name, last_name, role, group_id, is_active, created_at, updated_at
		FROM users WHERE group_id = ?
	`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []*User
	for rows.Next() {
		user, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	return users, rows.Err()
}

// GetAdmins получает всех администраторов группы
func (s *Storage) GetAdmins(ctx context.Context, groupID int64) ([]*User, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, telegram_id, username, first_name, last_name, role, group_id, is_active, created_at, updated_at
		FROM users WHERE group_id = ? AND role IN ('admin', 'owner') AND is_active = 1
	`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []*User
	for rows.Next() {
		user, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	return users, rows.Err()
}

// DeactivateUser деактивирует пользователя
func (s *Storage) DeactivateUser(ctx context.Context, tx *sql.Tx, userID int64) error {
	now := time.Now().Unix()
	_, err := tx.ExecContext(ctx, `UPDATE users SET is_active = 0, updated_at = ? WHERE id = ?`, now, userID)
	return err
}

func scanUser(row interface{ Scan(...interface{}) error }) (*User, error) {
	var user User
	var groupID sql.NullInt64
	var username, firstName, lastName sql.NullString
	var createdAt, updatedAt int64

	err := row.Scan(&user.ID, &user.TelegramID, &username, &firstName, &lastName,
		&user.Role, &groupID, &user.IsActive, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}

	user.Username = username.String
	user.FirstName = firstName.String
	user.LastName = lastName.String
	if groupID.Valid {
		user.GroupID = &groupID.Int64
	}
	user.CreatedAt = time.Unix(createdAt, 0).UTC()
	user.UpdatedAt = time.Unix(updatedAt, 0).UTC()

	return &user, nil
}
