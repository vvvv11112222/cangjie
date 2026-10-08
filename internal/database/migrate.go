package database

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
)

var migrationName = regexp.MustCompile(`^\d{3}_.+\.sql$`)

type migration struct {
	Name     string
	Path     string
	Checksum string
	SQL      string
}

func RunMigrations(ctx context.Context, databaseURL, directory string) error {
	config, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		return fmt.Errorf("parse DATABASE_URL: invalid connection string")
	}
	config.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	conn, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		return fmt.Errorf("connect postgres: %w", err)
	}
	defer conn.Close(ctx)

	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(hashtext('teaching_schema_migrations'))`); err != nil {
		return fmt.Errorf("lock migrations: %w", err)
	}
	defer conn.Exec(context.Background(), `SELECT pg_advisory_unlock(hashtext('teaching_schema_migrations'))`) //nolint:errcheck

	if _, err := conn.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS public.schema_migrations (
			name text PRIMARY KEY,
			checksum text NOT NULL,
			applied_at timestamptz NOT NULL DEFAULT now()
		)`); err != nil {
		return fmt.Errorf("create migration history: %w", err)
	}

	var teachingExists bool
	if err := conn.QueryRow(ctx, `SELECT to_regnamespace('teaching') IS NOT NULL`).Scan(&teachingExists); err != nil {
		return fmt.Errorf("inspect teaching schema: %w", err)
	}
	var appliedCount int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM public.schema_migrations`).Scan(&appliedCount); err != nil {
		return fmt.Errorf("inspect migration history: %w", err)
	}
	if teachingExists && appliedCount == 0 {
		return fmt.Errorf("teaching schema exists without migration history; baseline it manually before running this command")
	}

	migrations, err := loadMigrations(directory)
	if err != nil {
		return err
	}
	applied, err := appliedMigrations(ctx, conn)
	if err != nil {
		return err
	}
	for _, item := range migrations {
		if checksum, ok := applied[item.Name]; ok {
			if checksum != item.Checksum {
				return fmt.Errorf("migration %s changed after it was applied", item.Name)
			}
			continue
		}
		if err := applyMigration(ctx, conn, item); err != nil {
			return err
		}
	}
	return nil
}

func loadMigrations(directory string) ([]migration, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, fmt.Errorf("read migration directory: %w", err)
	}
	var result []migration
	for _, entry := range entries {
		if entry.IsDir() || !migrationName.MatchString(entry.Name()) {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		body, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read migration %s: %w", entry.Name(), err)
		}
		sum := sha256.Sum256(body)
		result = append(result, migration{
			Name: entry.Name(), Path: path,
			Checksum: hex.EncodeToString(sum[:]), SQL: withoutTransactionControl(string(body)),
		})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	if len(result) == 0 {
		return nil, fmt.Errorf("no numbered SQL migrations found in %s", directory)
	}
	return result, nil
}

func withoutTransactionControl(sql string) string {
	var lines []string
	scanner := bufio.NewScanner(strings.NewReader(sql))
	for scanner.Scan() {
		trimmed := strings.TrimSpace(scanner.Text())
		if trimmed == "BEGIN;" || trimmed == "COMMIT;" {
			continue
		}
		lines = append(lines, scanner.Text())
	}
	return strings.Join(lines, "\n")
}

func appliedMigrations(ctx context.Context, conn *pgx.Conn) (map[string]string, error) {
	rows, err := conn.Query(ctx, `SELECT name, checksum FROM public.schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("read migration history: %w", err)
	}
	defer rows.Close()
	result := make(map[string]string)
	for rows.Next() {
		var name, checksum string
		if err := rows.Scan(&name, &checksum); err != nil {
			return nil, fmt.Errorf("scan migration history: %w", err)
		}
		result[name] = checksum
	}
	return result, rows.Err()
}

func applyMigration(ctx context.Context, conn *pgx.Conn, item migration) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin migration %s: %w", item.Name, err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, item.SQL); err != nil {
		return fmt.Errorf("apply migration %s: %w", item.Name, err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO public.schema_migrations (name, checksum) VALUES ($1, $2)`, item.Name, item.Checksum); err != nil {
		return fmt.Errorf("record migration %s: %w", item.Name, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit migration %s: %w", item.Name, err)
	}
	return nil
}
