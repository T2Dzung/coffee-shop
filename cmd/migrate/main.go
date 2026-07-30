package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/golang/glog"
	"github.com/lib/pq"
)

const applicationRole = "coffeeshop_app"

func main() {
	switch os.Getenv("MIGRATION_MODE") {
	case "bootstrap":
		masterURL, err := buildPostgresURL(
			os.Getenv("MASTER_DB_USER"),
			os.Getenv("MASTER_DB_PASSWORD"),
			os.Getenv("MASTER_DB_HOST"),
			os.Getenv("MASTER_DB_PORT"),
			"postgres",
		)
		if err != nil {
			glog.Fatalf("cmd/migrate: invalid master database inputs: %v", err)
		}
		if err := bootstrapApplicationRole(
			masterURL,
			os.Getenv("APP_DB_PASSWORD"),
		); err != nil {
			glog.Fatalf("cmd/migrate: database bootstrap failed: %v", err)
		}
		glog.Infoln("cmd/migrate: application database role is ready")
	case "migrate":
		if err := migrateSchema(os.Getenv("PG_URL"), os.Getenv("MIGRATION_PATH")); err != nil {
			glog.Fatalf("cmd/migrate: schema migration failed: %v", err)
		}
		glog.Infoln("cmd/migrate: database schema is ready")
	case "restore-drill":
		if err := runRestoreDrillFromEnvironment(); err != nil {
			glog.Fatalf("cmd/migrate: restore drill action failed: %v", err)
		}
	default:
		glog.Fatalf("cmd/migrate: MIGRATION_MODE must be bootstrap, migrate or restore-drill")
	}
}

func buildPostgresURL(username, password, host, port, database string) (string, error) {
	if username == "" || password == "" || host == "" || port == "" || database == "" {
		return "", errors.New("database username, password, host, port and name are required")
	}
	return (&url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(username, password),
		Host:     host + ":" + port,
		Path:     database,
		RawQuery: "sslmode=require",
	}).String(), nil
}

func requireTLSDSN(name, dsn string) error {
	if dsn == "" {
		return fmt.Errorf("%s is required", name)
	}
	if !strings.Contains(dsn, "sslmode=") {
		return fmt.Errorf("%s must explicitly select sslmode", name)
	}
	return nil
}

func bootstrapApplicationRole(masterURL, applicationPassword string) error {
	if err := requireTLSDSN("MASTER_PG_URL", masterURL); err != nil {
		return err
	}
	if applicationPassword == "" {
		return errors.New("APP_DB_PASSWORD is required")
	}

	db, err := sql.Open("postgres", masterURL)
	if err != nil {
		return fmt.Errorf("open master connection: %w", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("connect with master credential: %w", err)
	}

	bootstrapSQL := buildBootstrapSQL(applicationPassword)
	if _, err := db.ExecContext(ctx, bootstrapSQL); err != nil {
		return fmt.Errorf("create or update application role: %w", err)
	}
	return nil
}

func buildBootstrapSQL(applicationPassword string) string {
	passwordLiteral := pq.QuoteLiteral(applicationPassword)
	return fmt.Sprintf(`
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_catalog.pg_roles WHERE rolname = '%s') THEN
    CREATE ROLE %s LOGIN PASSWORD %s;
  ELSE
    ALTER ROLE %s LOGIN PASSWORD %s;
  END IF;
END
$$;
GRANT CONNECT ON DATABASE postgres TO %s;
GRANT CREATE ON DATABASE postgres TO %s;
GRANT USAGE, CREATE ON SCHEMA public TO %s;
`, applicationRole, applicationRole, passwordLiteral, applicationRole, passwordLiteral,
		applicationRole, applicationRole, applicationRole)
}

func migrateSchema(databaseURL, migrationPath string) error {
	if err := requireTLSDSN("PG_URL", databaseURL); err != nil {
		return err
	}
	if migrationPath == "" {
		migrationPath = "db/migrations"
	}

	if !filepath.IsAbs(migrationPath) {
		currentDirectory, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("resolve current directory: %w", err)
		}
		migrationPath = filepath.Join(currentDirectory, migrationPath)
	}
	sourceURL := "file://" + filepath.ToSlash(migrationPath)

	var migration *migrate.Migrate
	var err error
	for attempt := 1; attempt <= 5; attempt++ {
		migration, err = migrate.New(sourceURL, databaseURL)
		if err == nil {
			break
		}
		if attempt < 5 {
			time.Sleep(2 * time.Second)
		}
	}
	if err != nil {
		return fmt.Errorf("initialize migration after retries: %w", err)
	}
	defer migration.Close()

	if err := migration.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}
