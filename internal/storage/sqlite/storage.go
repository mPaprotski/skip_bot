package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

// Storage обёртка над sql.DB
type Storage struct {
	DB *sql.DB
}

// New создаёт новое подключение к SQLite
func New(dbPath string) (*Storage, error) {
	if dbPath != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(dbPath), 0700); err != nil {
			return nil, err
		}
	}
	sep := "?"
	if strings.Contains(dbPath, "?") {
		sep = "&"
	}
	db, err := sql.Open("sqlite", dbPath+sep+"_pragma="+url.QueryEscape("foreign_keys(1)")+"&_pragma="+url.QueryEscape("busy_timeout(5000)"))
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Ограничиваем пул одним соединением для SQLite
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	// Применяем PRAGMA
	if err := applyPragmas(db); err != nil {
		db.Close()
		return nil, err
	}

	return &Storage{DB: db}, nil
}

func applyPragmas(db *sql.DB) error {
	pragmas := []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA journal_mode = WAL",
		"PRAGMA busy_timeout = 5000",
		"PRAGMA synchronous = NORMAL",
	}

	for _, pragma := range pragmas {
		if _, err := db.Exec(pragma); err != nil {
			return fmt.Errorf("failed to apply %s: %w", pragma, err)
		}
	}
	return nil
}

// Close закрывает подключение
func (s *Storage) Close() error {
	return s.DB.Close()
}

// WithTx выполняет функцию в транзакции
func (s *Storage) WithTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}

	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}

	return tx.Commit()
}
