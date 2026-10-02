package sqlite

import (
	"context"
	"database/sql"
	"time"
)

// AdminLog представляет запись в журнале административных действий
type AdminLog struct {
	ID         int64
	AdminID    int64
	Action     string
	EntityType string
	EntityID   *int64
	Details    *string
	CreatedAt  time.Time
}

// CreateAdminLog создаёт запись в журнале
func (s *Storage) CreateAdminLog(ctx context.Context, tx *sql.Tx, log *AdminLog) error {
	now := time.Now().Unix()
	result, err := tx.ExecContext(ctx, `
		INSERT INTO admin_logs (admin_id, action, entity_type, entity_id, details, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, log.AdminID, log.Action, log.EntityType, log.EntityID, log.Details, now)
	if err != nil {
		return err
	}
	log.ID, _ = result.LastInsertId()
	return nil
}

// GetAdminLogs получает последние записи журнала
func (s *Storage) GetAdminLogs(ctx context.Context, limit int) ([]*AdminLog, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, admin_id, action, entity_type, entity_id, details, created_at
		FROM admin_logs ORDER BY created_at DESC LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var logs []*AdminLog
	for rows.Next() {
		log, err := scanAdminLog(rows)
		if err != nil {
			return nil, err
		}
		logs = append(logs, log)
	}
	return logs, rows.Err()
}

func scanAdminLog(row interface{ Scan(...interface{}) error }) (*AdminLog, error) {
	var log AdminLog
	var entityID sql.NullInt64
	var details sql.NullString
	var createdAt int64

	err := row.Scan(&log.ID, &log.AdminID, &log.Action, &log.EntityType, &entityID, &details, &createdAt)
	if err != nil {
		return nil, err
	}
	if entityID.Valid {
		log.EntityID = &entityID.Int64
	}
	if details.Valid {
		log.Details = &details.String
	}
	log.CreatedAt = time.Unix(createdAt, 0).UTC()
	return &log, nil
}
