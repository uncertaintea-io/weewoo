package migrations

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed sql/*.sql sqlite/*.sql
var migrationFS embed.FS

// This lock ID is stable for WeeWoo migrations and prevents two replicas from
// changing the schema at the same time.
const advisoryLockID int64 = 0x776565776f6f

const createSchemaMigrationsTableSQL = `
CREATE TABLE IF NOT EXISTS schema_migrations (
	version BIGINT PRIMARY KEY,
	name TEXT NOT NULL,
	applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
)`

type migration struct {
	Version int64
	Name    string
	SQL     string
}

type Status struct {
	Version   int64
	Name      string
	Applied   bool
	AppliedAt *time.Time
}

// Apply applies every pending embedded migration in version order. Calls from
// concurrent processes are serialized with a PostgreSQL advisory lock.
func Apply(ctx context.Context, db *sql.DB, database string) error {
	if normalizedDatabase(database) == "sqlite" {
		return applyWithoutAdvisoryLock(ctx, db)
	}
	if normalizedDatabase(database) != "postgresql" {
		return fmt.Errorf("unsupported database %q", database)
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire migration connection: %w", err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, advisoryLockID); err != nil {
		return fmt.Errorf("acquire migration lock: %w", err)
	}
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := conn.ExecContext(unlockCtx, `SELECT pg_advisory_unlock($1)`, advisoryLockID); err != nil {
			slog.Warn("failed to release migration lock", slog.Any("error", err))
		}
	}()

	if _, err := conn.ExecContext(ctx, createSchemaMigrationsTableSQL); err != nil {
		return fmt.Errorf("ensure schema_migrations table: %w", err)
	}

	items, err := load()
	if err != nil {
		return err
	}
	applied, err := appliedMigrations(ctx, conn)
	if err != nil {
		return err
	}

	for _, item := range items {
		if appliedName, ok := applied[item.Version]; ok {
			if appliedName != item.Name {
				return fmt.Errorf("migration %d was applied as %q but is now named %q", item.Version, appliedName, item.Name)
			}
			continue
		}

		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin migration %d: %w", item.Version, err)
		}
		if _, err := tx.ExecContext(ctx, item.SQL); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration %d (%s): %w", item.Version, item.Name, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO schema_migrations (version, name, applied_at) VALUES ($1, $2, NOW())`,
			item.Version, item.Name,
		); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record migration %d (%s): %w", item.Version, item.Name, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %d (%s): %w", item.Version, item.Name, err)
		}
		slog.Info("database migration applied", "version", item.Version, "name", item.Name)
	}
	return nil
}

func applyWithoutAdvisoryLock(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, createSchemaMigrationsTableSQL); err != nil {
		return fmt.Errorf("ensure schema_migrations table: %w", err)
	}
	items, err := loadFor("sqlite")
	if err != nil {
		return err
	}
	applied, err := appliedMigrations(ctx, db)
	if err != nil {
		return err
	}
	for _, item := range items {
		if appliedName, ok := applied[item.Version]; ok {
			if appliedName != item.Name {
				return fmt.Errorf("migration %d was applied as %q but is now named %q", item.Version, appliedName, item.Name)
			}
			continue
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin migration %d: %w", item.Version, err)
		}
		if _, err := tx.ExecContext(ctx, item.SQL); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration %d (%s): %w", item.Version, item.Name, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version, name, applied_at) VALUES ($1, $2, CURRENT_TIMESTAMP)`, item.Version, item.Name); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record migration %d (%s): %w", item.Version, item.Name, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %d (%s): %w", item.Version, item.Name, err)
		}
		slog.Info("database migration applied", "version", item.Version, "name", item.Name)
	}
	return nil
}

// Statuses reports embedded migrations and whether each has been applied.
func Statuses(ctx context.Context, db *sql.DB, database string) ([]Status, error) {
	if _, err := db.ExecContext(ctx, createSchemaMigrationsTableSQL); err != nil {
		return nil, fmt.Errorf("ensure schema_migrations table: %w", err)
	}
	items, err := loadFor(database)
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT version, name, applied_at FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("query applied migrations: %w", err)
	}
	defer rows.Close()
	applied := make(map[int64]Status)
	for rows.Next() {
		var status Status
		if err := rows.Scan(&status.Version, &status.Name, &status.AppliedAt); err != nil {
			return nil, fmt.Errorf("scan applied migration: %w", err)
		}
		status.Applied = true
		applied[status.Version] = status
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate applied migrations: %w", err)
	}

	statuses := make([]Status, 0, len(items))
	for _, item := range items {
		status := Status{Version: item.Version, Name: item.Name}
		if existing, ok := applied[item.Version]; ok {
			status.Applied = true
			status.AppliedAt = existing.AppliedAt
		}
		statuses = append(statuses, status)
	}
	return statuses, nil
}

type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func appliedMigrations(ctx context.Context, db queryer) (map[int64]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT version, name FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("query applied migrations: %w", err)
	}
	defer rows.Close()
	applied := make(map[int64]string)
	for rows.Next() {
		var version int64
		var name string
		if err := rows.Scan(&version, &name); err != nil {
			return nil, fmt.Errorf("scan applied migration: %w", err)
		}
		applied[version] = name
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate applied migrations: %w", err)
	}
	return applied, nil
}

func load() ([]migration, error) {
	return loadFor("postgresql")
}

func loadFor(database string) ([]migration, error) {
	var directory string
	switch normalizedDatabase(database) {
	case "postgresql":
		directory = "sql"
	case "sqlite":
		directory = "sqlite"
	default:
		return nil, fmt.Errorf("unsupported database %q", database)
	}
	entries, err := fs.ReadDir(migrationFS, directory)
	if err != nil {
		return nil, fmt.Errorf("read migrations: %w", err)
	}
	items := make([]migration, 0, len(entries))
	seen := make(map[int64]string)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		version, name, err := parseFilename(entry.Name())
		if err != nil {
			return nil, err
		}
		if previous, ok := seen[version]; ok {
			return nil, fmt.Errorf("duplicate migration version %d in %q and %q", version, previous, entry.Name())
		}
		body, err := fs.ReadFile(migrationFS, directory+"/"+entry.Name())
		if err != nil {
			return nil, fmt.Errorf("read migration %q: %w", entry.Name(), err)
		}
		seen[version] = entry.Name()
		items = append(items, migration{Version: version, Name: name, SQL: string(body)})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Version < items[j].Version })
	return items, nil
}

func normalizedDatabase(database string) string {
	return strings.ToLower(strings.TrimSpace(database))
}

func parseFilename(filename string) (int64, string, error) {
	stem := strings.TrimSuffix(filename, ".sql")
	parts := strings.SplitN(stem, "_", 2)
	if len(parts) != 2 || parts[1] == "" {
		return 0, "", fmt.Errorf("migration filename %q must contain a numeric version and name", filename)
	}
	version, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || version <= 0 {
		return 0, "", fmt.Errorf("migration filename %q must start with a positive numeric version", filename)
	}
	return version, parts[1], nil
}
