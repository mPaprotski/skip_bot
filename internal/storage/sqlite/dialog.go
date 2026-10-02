package sqlite

import (
	"context"
	"database/sql"
	"time"
)

// DialogState представляет состояние диалога
type DialogState struct {
	ID          int64
	UserID      int64
	DialogType  string
	CurrentStep int
	Data        string // JSON
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// GetDialogState получает состояние диалога пользователя
func (s *Storage) GetDialogState(ctx context.Context, userID int64, dialogType string) (*DialogState, error) {
	row := s.DB.QueryRowContext(ctx, `
		SELECT id, user_id, dialog_type, current_step, data, created_at, updated_at
		FROM dialog_states WHERE user_id = ? AND dialog_type = ?
	`, userID, dialogType)
	return scanDialogState(row)
}

// SaveDialogState сохраняет состояние диалога
func (s *Storage) SaveDialogState(ctx context.Context, tx *sql.Tx, state *DialogState) error {
	now := time.Now().Unix()

	// Проверяем, существует ли уже состояние
	var exists bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM dialog_states WHERE user_id = ? AND dialog_type = ?)`,
		state.UserID, state.DialogType).Scan(&exists)
	if err != nil {
		return err
	}

	if exists {
		_, err = tx.ExecContext(ctx, `
			UPDATE dialog_states SET current_step = ?, data = ?, updated_at = ?
			WHERE user_id = ? AND dialog_type = ?
		`, state.CurrentStep, state.Data, now, state.UserID, state.DialogType)
	} else {
		result, err := tx.ExecContext(ctx, `
			INSERT INTO dialog_states (user_id, dialog_type, current_step, data, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?)
		`, state.UserID, state.DialogType, state.CurrentStep, state.Data, now, now)
		if err != nil {
			return err
		}
		state.ID, _ = result.LastInsertId()
	}
	return err
}

// DeleteDialogState удаляет состояние диалога
func (s *Storage) DeleteDialogState(ctx context.Context, tx *sql.Tx, userID int64, dialogType string) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM dialog_states WHERE user_id = ? AND dialog_type = ?`, userID, dialogType)
	return err
}

// DeleteAllDialogStates удаляет все состояния диалогов пользователя
func (s *Storage) DeleteAllDialogStates(ctx context.Context, tx *sql.Tx, userID int64) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM dialog_states WHERE user_id = ?`, userID)
	return err
}

func scanDialogState(row interface{ Scan(...interface{}) error }) (*DialogState, error) {
	var state DialogState
	var createdAt, updatedAt int64

	err := row.Scan(&state.ID, &state.UserID, &state.DialogType, &state.CurrentStep, &state.Data, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	state.CreatedAt = time.Unix(createdAt, 0).UTC()
	state.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return &state, nil
}
