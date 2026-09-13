package store

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"strings"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// migrationLock is the advisory lock a migrating process holds, so that two
// harness processes starting at the same time apply each migration once.
const migrationLock int64 = 5310923

// migrate applies every embedded migration the database has not recorded yet,
// in file name order, each one in a transaction together with its
// schema_migrations row.
func (s *Store) migrate(ctx context.Context) error {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return wrap("acquire migration connection", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, migrationLock); err != nil {
		return wrap("lock migrations", err)
	}
	defer func() { _, _ = conn.Exec(ctx, `SELECT pg_advisory_unlock($1)`, migrationLock) }()

	const createTable = `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    text PRIMARY KEY,
		applied_at timestamptz NOT NULL DEFAULT now()
	)`
	if _, err := conn.Exec(ctx, createTable); err != nil {
		return wrap("create schema_migrations", err)
	}

	applied, err := appliedMigrations(ctx, conn)
	if err != nil {
		return err
	}
	names, err := migrationNames()
	if err != nil {
		return err
	}
	for _, name := range names {
		version := strings.TrimSuffix(name, ".sql")
		if applied[version] {
			continue
		}
		sql, err := migrationFiles.ReadFile("migrations/" + name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}
		tx, err := conn.Begin(ctx)
		if err != nil {
			return wrap("begin migration "+version, err)
		}
		if _, err := tx.Exec(ctx, string(sql)); err != nil {
			_ = tx.Rollback(ctx)
			return wrap("apply migration "+version, err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, version); err != nil {
			_ = tx.Rollback(ctx)
			return wrap("record migration "+version, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return wrap("commit migration "+version, err)
		}
	}
	return nil
}

// appliedMigrations reads the versions the database already carries.
func appliedMigrations(ctx context.Context, q querier) (map[string]bool, error) {
	rows, err := q.Query(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, wrap("read schema_migrations", err)
	}
	defer rows.Close()
	applied := map[string]bool{}
	for rows.Next() {
		var version string
		if err := rows.Scan(&version); err != nil {
			return nil, wrap("scan schema_migrations", err)
		}
		applied[version] = true
	}
	if err := rows.Err(); err != nil {
		return nil, wrap("read schema_migrations", err)
	}
	return applied, nil
}

// migrationNames lists the embedded migrations in the order they apply, which
// is the order their file names sort in.
func migrationNames() ([]string, error) {
	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		return nil, fmt.Errorf("read migrations: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	return names, nil
}
