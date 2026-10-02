package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"net/url"

	"github.com/tursodatabase/libsql-client-go/libsql"
)

// NewTurso uses the libSQL wire protocol without a local database or CGO.
func NewTurso(ctx context.Context, databaseURL, authToken string) (*Storage, error) {
	u, e := url.Parse(databaseURL)
	if e != nil || (u.Scheme != "libsql" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || authToken == "" {
		return nil, errors.New("invalid Turso URL or missing authentication token")
	}
	connector, e := libsql.NewConnector(databaseURL, libsql.WithAuthToken(authToken))
	if e != nil {
		return nil, errors.New("failed to configure Turso connection")
	}
	db := sql.OpenDB(foreignKeyConnector{connector})
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if e = db.PingContext(ctx); e != nil {
		db.Close()
		return nil, errors.New("Turso is unavailable; check database URL, token and network access")
	}
	return &Storage{DB: db, Remote: true}, nil
}

// Initialize every remote connection, including reconnects. WAL and other
// filesystem pragmas are only configured by the local SQLite constructor.
type foreignKeyConnector struct{ driver.Connector }

func (c foreignKeyConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, e := c.Connector.Connect(ctx)
	if e != nil {
		return nil, e
	}
	executor, ok := conn.(driver.ExecerContext)
	if !ok {
		conn.Close()
		return nil, errors.New("Turso driver does not support context-aware execution")
	}
	if _, e = executor.ExecContext(ctx, "PRAGMA foreign_keys = ON", nil); e != nil {
		conn.Close()
		return nil, e
	}
	return conn, nil
}
