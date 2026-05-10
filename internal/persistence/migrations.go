package persistence

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

func runMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version TEXT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)
	`)
	if err != nil {
		return fmt.Errorf("create schema_migrations table: %w", err)
	}

	entries, err := fs.Glob(migrationFiles, "migrations/*.sql")
	if err != nil {
		return fmt.Errorf("list migration files: %w", err)
	}
	sort.Strings(entries)

	for _, entry := range entries {
		version := filepath.Base(entry)
		applied, err := isMigrationApplied(ctx, pool, version)
		if err != nil {
			return err
		}
		if applied {
			continue
		}

		content, err := migrationFiles.ReadFile(entry)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", version, err)
		}

		tx, err := pool.Begin(ctx)
		if err != nil {
			return fmt.Errorf("begin migration transaction %s: %w", version, err)
		}

		if _, err := tx.Exec(ctx, string(content)); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("execute migration %s: %w", version, err)
		}

		if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, version); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("record migration %s: %w", version, err)
		}

		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit migration %s: %w", version, err)
		}
	}

	return nil
}

func isMigrationApplied(ctx context.Context, pool *pgxpool.Pool, version string) (bool, error) {
	var exists bool
	if err := pool.QueryRow(
		ctx,
		`SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)`,
		version,
	).Scan(&exists); err != nil {
		return false, fmt.Errorf("check migration %s: %w", version, err)
	}

	return exists, nil
}
