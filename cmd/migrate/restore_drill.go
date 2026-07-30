package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

const restoreDrillSchema = "platform_restore_drill"

var restoreDrillIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{7,62}$`)

var expectedApplicationTables = []string{
	`barista.barista_orders`,
	`kitchen.kitchen_orders`,
	`order.line_items`,
	`order.orders`,
}

type restoreDrillInput struct {
	Action           string
	DrillID          string
	Payload          string
	ExpectedChecksum string
}

type restoreDrillOutput struct {
	Action            string    `json:"action"`
	DrillID           string    `json:"drill_id"`
	RestoreTime       time.Time `json:"restore_time,omitempty"`
	MarkerAPresent    bool      `json:"marker_a_present,omitempty"`
	MarkerBPresent    bool      `json:"marker_b_present,omitempty"`
	Checksum          string    `json:"checksum,omitempty"`
	ApplicationTables []string  `json:"application_tables,omitempty"`
	Cleaned           bool      `json:"cleaned,omitempty"`
}

type restoreDrillStore interface {
	WriteMarkerA(context.Context, string, string) (time.Time, string, error)
	WriteMarkerB(context.Context, string, string) error
	Validate(context.Context, string, string) (restoreDrillOutput, error)
	Cleanup(context.Context, string) error
}

type postgresRestoreDrillStore struct{ db *sql.DB }

func runRestoreDrillFromEnvironment() error {
	dsn := os.Getenv("PG_URL")
	if dsn == "" {
		var err error
		dsn, err = buildPostgresURL(
			os.Getenv("DB_USER"), os.Getenv("DB_PASSWORD"), os.Getenv("DB_HOST"),
			os.Getenv("DB_PORT"), os.Getenv("DB_NAME"),
		)
		if err != nil {
			return err
		}
	}
	if err := requireTLSDSN("restore drill database URL", dsn); err != nil {
		return err
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return fmt.Errorf("open restore drill database: %w", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("connect to restore drill database: %w", err)
	}
	output, err := runRestoreDrill(ctx, postgresRestoreDrillStore{db: db}, restoreDrillInput{
		Action:           os.Getenv("RESTORE_DRILL_ACTION"),
		DrillID:          os.Getenv("RESTORE_DRILL_ID"),
		Payload:          os.Getenv("RESTORE_DRILL_PAYLOAD"),
		ExpectedChecksum: os.Getenv("RESTORE_DRILL_EXPECTED_CHECKSUM"),
	})
	if err != nil {
		return err
	}
	data, err := json.Marshal(output)
	if err != nil {
		return fmt.Errorf("encode restore drill result: %w", err)
	}
	fmt.Println(string(data))
	return nil
}

func runRestoreDrill(ctx context.Context, store restoreDrillStore, input restoreDrillInput) (restoreDrillOutput, error) {
	if store == nil {
		return restoreDrillOutput{}, errors.New("restore drill store is required")
	}
	if !restoreDrillIDPattern.MatchString(input.DrillID) {
		return restoreDrillOutput{}, errors.New("RESTORE_DRILL_ID must be 8-63 lowercase letters, digits or hyphens")
	}
	base := restoreDrillOutput{Action: input.Action, DrillID: input.DrillID}
	switch input.Action {
	case "probe":
		return base, nil
	case "write-a":
		if input.Payload == "" {
			return restoreDrillOutput{}, errors.New("RESTORE_DRILL_PAYLOAD is required for write-a")
		}
		restoreTime, checksum, err := store.WriteMarkerA(ctx, input.DrillID, input.Payload)
		base.RestoreTime, base.Checksum = restoreTime.UTC(), checksum
		return base, err
	case "write-b":
		if input.Payload == "" {
			return restoreDrillOutput{}, errors.New("RESTORE_DRILL_PAYLOAD is required for write-b")
		}
		return base, store.WriteMarkerB(ctx, input.DrillID, input.Payload)
	case "validate":
		if input.ExpectedChecksum == "" {
			return restoreDrillOutput{}, errors.New("RESTORE_DRILL_EXPECTED_CHECKSUM is required for validate")
		}
		return store.Validate(ctx, input.DrillID, input.ExpectedChecksum)
	case "cleanup":
		if err := store.Cleanup(ctx, input.DrillID); err != nil {
			return restoreDrillOutput{}, err
		}
		base.Cleaned = true
		return base, nil
	default:
		return restoreDrillOutput{}, errors.New("RESTORE_DRILL_ACTION must be probe, write-a, write-b, validate or cleanup")
	}
}

func (s postgresRestoreDrillStore) WriteMarkerA(ctx context.Context, drillID, payload string) (time.Time, string, error) {
	if err := s.insertMarker(ctx, drillID, "A", payload, true); err != nil {
		return time.Time{}, "", err
	}
	var restoreTime time.Time
	var stored string
	if err := s.db.QueryRowContext(ctx, `
SELECT clock_timestamp(), payload
FROM platform_restore_drill.marker
WHERE drill_id = $1 AND marker_name = 'A'`, drillID).Scan(&restoreTime, &stored); err != nil {
		return time.Time{}, "", fmt.Errorf("verify committed marker A: %w", err)
	}
	return restoreTime.UTC(), markerChecksum(drillID, "A", stored), nil
}

func (s postgresRestoreDrillStore) WriteMarkerB(ctx context.Context, drillID, payload string) error {
	return s.insertMarker(ctx, drillID, "B", payload, false)
}

func (s postgresRestoreDrillStore) insertMarker(ctx context.Context, drillID, marker, payload string, create bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin marker %s transaction: %w", marker, err)
	}
	defer tx.Rollback()
	if create {
		if _, err := tx.ExecContext(ctx, `
CREATE SCHEMA IF NOT EXISTS platform_restore_drill;
CREATE TABLE IF NOT EXISTS platform_restore_drill.marker (
  drill_id text NOT NULL,
  marker_name text NOT NULL,
  payload text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  PRIMARY KEY (drill_id, marker_name)
)`); err != nil {
			return fmt.Errorf("create isolated restore drill marker table: %w", err)
		}
	}
	result, err := tx.ExecContext(ctx, `
INSERT INTO platform_restore_drill.marker (drill_id, marker_name, payload)
VALUES ($1, $2, $3)
ON CONFLICT (drill_id, marker_name) DO NOTHING`, drillID, marker, payload)
	if err != nil {
		return fmt.Errorf("insert marker %s: %w", marker, err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read marker %s insert result: %w", marker, err)
	}
	if inserted == 0 {
		var existing string
		if err := tx.QueryRowContext(ctx, `SELECT payload FROM platform_restore_drill.marker
WHERE drill_id = $1 AND marker_name = $2`, drillID, marker).Scan(&existing); err != nil {
			return fmt.Errorf("read existing marker %s: %w", marker, err)
		}
		if existing != payload {
			return fmt.Errorf("marker %s already exists with a different payload", marker)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit marker %s: %w", marker, err)
	}
	return nil
}

func (s postgresRestoreDrillStore) Validate(ctx context.Context, drillID, expectedChecksum string) (restoreDrillOutput, error) {
	output := restoreDrillOutput{Action: "validate", DrillID: drillID}
	rows, err := s.db.QueryContext(ctx, `SELECT marker_name, payload
FROM platform_restore_drill.marker WHERE drill_id = $1 ORDER BY marker_name`, drillID)
	if err != nil {
		return output, fmt.Errorf("query restore drill markers: %w", err)
	}
	defer rows.Close()
	markerCount := map[string]int{}
	for rows.Next() {
		var marker, payload string
		if err := rows.Scan(&marker, &payload); err != nil {
			return output, fmt.Errorf("scan restore drill marker: %w", err)
		}
		markerCount[marker]++
		if marker == "A" {
			output.Checksum = markerChecksum(drillID, marker, payload)
		}
	}
	if err := rows.Err(); err != nil {
		return output, fmt.Errorf("iterate restore drill markers: %w", err)
	}
	output.MarkerAPresent = markerCount["A"] == 1
	output.MarkerBPresent = markerCount["B"] > 0
	if !output.MarkerAPresent || output.MarkerBPresent || output.Checksum != expectedChecksum {
		return output, fmt.Errorf("restore boundary mismatch: marker_a=%t marker_b=%t checksum_match=%t",
			output.MarkerAPresent, output.MarkerBPresent, output.Checksum == expectedChecksum)
	}
	for _, table := range expectedApplicationTables {
		var exists bool
		parts := strings.SplitN(table, ".", 2)
		if len(parts) != 2 {
			return output, fmt.Errorf("invalid expected application table %s", table)
		}
		if err := s.db.QueryRowContext(ctx, `SELECT EXISTS (
  SELECT 1 FROM pg_catalog.pg_class c
  JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
  WHERE n.nspname = $1 AND c.relname = $2 AND c.relkind IN ('r', 'p')
)`, parts[0], parts[1]).Scan(&exists); err != nil {
			return output, fmt.Errorf("check application table %s: %w", table, err)
		}
		if !exists {
			return output, fmt.Errorf("expected application table %s is absent", table)
		}
		output.ApplicationTables = append(output.ApplicationTables, table)
	}
	sort.Strings(output.ApplicationTables)
	return output, nil
}

func (s postgresRestoreDrillStore) Cleanup(ctx context.Context, drillID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin marker cleanup: %w", err)
	}
	defer tx.Rollback()
	var markerTableExists bool
	if err := tx.QueryRowContext(ctx, `SELECT to_regclass('platform_restore_drill.marker') IS NOT NULL`).Scan(&markerTableExists); err != nil {
		return fmt.Errorf("check restore drill marker table: %w", err)
	}
	if !markerTableExists {
		return tx.Commit()
	}
	if _, err := tx.ExecContext(ctx, `LOCK TABLE platform_restore_drill.marker IN ACCESS EXCLUSIVE MODE`); err != nil {
		return fmt.Errorf("lock restore drill marker table: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM platform_restore_drill.marker WHERE drill_id = $1`, drillID); err != nil {
		return fmt.Errorf("delete exact restore drill markers: %w", err)
	}
	var remaining int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM platform_restore_drill.marker`).Scan(&remaining); err != nil {
		return fmt.Errorf("count remaining restore drill markers: %w", err)
	}
	if remaining == 0 {
		if _, err := tx.ExecContext(ctx, `DROP SCHEMA platform_restore_drill CASCADE`); err != nil {
			return fmt.Errorf("drop empty restore drill schema: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit marker cleanup: %w", err)
	}
	return nil
}

func markerChecksum(drillID, marker, payload string) string {
	digest := sha256.Sum256([]byte(drillID + "\x00" + marker + "\x00" + payload))
	return hex.EncodeToString(digest[:])
}
