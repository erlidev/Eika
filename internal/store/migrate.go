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
// in file name order.
//
// The whole run is one transaction, and the advisory lock is a transaction
// lock, so it is released when the transaction ends however it ends: a
// cancelled context cannot leave a connection holding the lock back in the
// pool. A failed migration leaves the schema as it was.
func (s *Store) migrate(ctx context.Context) error {
	return s.tx(ctx, func(q querier) error {
		if _, err := q.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, migrationLock); err != nil {
			return wrap("lock migrations", err)
		}
		const createTable = `CREATE TABLE IF NOT EXISTS schema_migrations (
			version    text PRIMARY KEY,
			applied_at timestamptz NOT NULL DEFAULT now()
		)`
		if _, err := q.Exec(ctx, createTable); err != nil {
			return wrap("create schema_migrations", err)
		}
		applied, err := appliedMigrations(ctx, q)
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
			if _, err := q.Exec(ctx, string(sql)); err != nil {
				return wrap("apply migration "+version, err)
			}
			if _, err := q.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, version); err != nil {
				return wrap("record migration "+version, err)
			}
		}
		return nil
	})
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
