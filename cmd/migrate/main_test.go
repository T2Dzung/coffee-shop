package main

import (
	"strings"
	"testing"
)

func TestBuildPostgresURLEncodesCredentialsAndRequiresTLS(t *testing.T) {
	dsn, err := buildPostgresURL("postgres", "p@ss:/word", "db.internal", "5432", "postgres")
	if err != nil {
		t.Fatalf("buildPostgresURL() error = %v", err)
	}
	if strings.Contains(dsn, "p@ss:/word") {
		t.Fatalf("password was not URL-encoded: %q", dsn)
	}
	if want := "sslmode=require"; !strings.Contains(dsn, want) {
		t.Fatalf("DSN %q does not contain %q", dsn, want)
	}
	if err := requireTLSDSN("PG_URL", dsn); err != nil {
		t.Fatalf("requireTLSDSN() rejected generated DSN: %v", err)
	}
}

func TestRequireTLSDSNRejectsIncompleteInput(t *testing.T) {
	for name, dsn := range map[string]string{
		"empty":       "",
		"no ssl mode": "postgres://user:password@db.internal:5432/postgres",
	} {
		t.Run(name, func(t *testing.T) {
			if err := requireTLSDSN("PG_URL", dsn); err == nil {
				t.Fatal("requireTLSDSN() error = nil, want rejection")
			}
		})
	}
}

func TestBuildBootstrapSQLGrantsDatabaseSchemaCreation(t *testing.T) {
	statement := buildBootstrapSQL("p'ass")
	for _, required := range []string{
		"GRANT CONNECT ON DATABASE postgres TO coffeeshop_app;",
		"GRANT CREATE ON DATABASE postgres TO coffeeshop_app;",
		"GRANT USAGE, CREATE ON SCHEMA public TO coffeeshop_app;",
		"PASSWORD 'p''ass'",
	} {
		if !strings.Contains(statement, required) {
			t.Errorf("bootstrap SQL is missing %q", required)
		}
	}
}
