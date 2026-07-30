package prod

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	platformaws "github.com/thangchung/go-coffeeshop/internal/platformctl/aws"
	"github.com/thangchung/go-coffeeshop/internal/platformctl/command"
	platformconfig "github.com/thangchung/go-coffeeshop/internal/platformctl/config"
	"gopkg.in/yaml.v3"
)

const testMigrationImage = "123456789012.dkr.ecr.ap-southeast-1.amazonaws.com/go-coffeeshop-migrate@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestRestoreDrillTargetJobUsesSecretReferenceAndBoundedRuntime(t *testing.T) {
	t.Parallel()
	state := RestoreDrillState{
		DrillID: "o3-drill-1", MigrationImage: testMigrationImage, MarkerChecksum: "checksum",
		SourcePort: 5432,
	}
	manifest, err := restoreDrillJobManifest("restore-drill-validate-o3-drill-1", "validate", "target.internal", state, false)
	require.NoError(t, err)
	require.NotContains(t, string(manifest), "password-value")
	require.Contains(t, string(manifest), "APP_DB_PASSWORD")
	require.Contains(t, string(manifest), "secretKeyRef")
	require.Contains(t, string(manifest), "backoffLimit: 0")
	require.Contains(t, string(manifest), "activeDeadlineSeconds: 180")
	require.Contains(t, string(manifest), "@sha256:")

	var document map[string]any
	require.NoError(t, yaml.Unmarshal(manifest, &document))
	require.Equal(t, "batch/v1", document["apiVersion"])
}

func TestRestoreDrillSourceJobDoesNotRenderDatabaseURL(t *testing.T) {
	t.Parallel()
	state := RestoreDrillState{DrillID: "o3-drill-1", MigrationImage: testMigrationImage, MarkerPayload: "marker"}
	manifest, err := restoreDrillJobManifest("restore-drill-write-a-o3-drill-1", "write-a", "", state, true)
	require.NoError(t, err)
	require.Contains(t, string(manifest), "key: PG_URL")
	require.NotContains(t, string(manifest), "postgres://")
}

func TestRestoreDrillJobRejectsMutableImage(t *testing.T) {
	t.Parallel()
	_, err := restoreDrillJobManifest("job", "probe", "", RestoreDrillState{
		DrillID: "o3-drill-1", MigrationImage: "repo:latest",
	}, true)
	require.ErrorContains(t, err, "immutable")
	_, err = restoreDrillJobManifest("job", "probe", "", RestoreDrillState{
		DrillID: "o3-drill-1", MigrationImage: testMigrationImage + "-trailing",
	}, true)
	require.ErrorContains(t, err, "immutable")
}

func TestDecodeRestoreDrillJobResultUsesStructuredFinalLine(t *testing.T) {
	t.Parallel()
	logs := strings.Join([]string{"migration log", `{"action":"write-a","drill_id":"o3-drill-1","restore_time":"2026-07-30T01:00:00Z","checksum":"abc"}`}, "\n")
	result, err := decodeRestoreDrillJobResult(logs)
	require.NoError(t, err)
	require.Equal(t, "abc", result.Checksum)
}

func TestRestoreDrillCleanupRejectsTargetTagMismatch(t *testing.T) {
	t.Parallel()
	runner := &command.FakeRunner{Expectations: []command.Expectation{{
		Name: "aws", Args: []string{"rds", "list-tags-for-resource", "--resource-name", "arn:target", "--output", "json"},
		Result: command.Result{Stdout: `{"TagList":[{"Key":"Purpose","Value":"someone-else"}]}`},
	}}}
	operations := NewRealRestoreDrillOperations(&RealOperations{AWS: platformaws.Client{Runner: runner}})
	err := operations.verifyCleanupTargetIdentity(context.Background(), RestoreDrillState{
		DrillID:  "o3-drill-1",
		SourceID: "source", TargetID: "target", TargetARN: "arn:target", TargetResourceID: "db-target",
		Tags: map[string]string{"Purpose": "restore-drill", "DrillID": "o3-drill-1"},
	}, platformaws.RDSInstance{
		Identifier: "target", ARN: "arn:target", ResourceID: "db-target",
	})
	require.ErrorContains(t, err, "target RDS ownership tag")
	require.NoError(t, runner.Verify())
}

func TestRestoreDrillCleanupAcceptsOwnedFailedTarget(t *testing.T) {
	t.Parallel()
	runner := &command.FakeRunner{Expectations: []command.Expectation{{
		Name: "aws", Args: []string{"rds", "list-tags-for-resource", "--resource-name", "arn:target", "--output", "json"},
		Result: command.Result{Stdout: `{"TagList":[{"Key":"Purpose","Value":"restore-drill"},{"Key":"DrillID","Value":"o3-drill-1"}]}`},
	}}}
	operations := NewRealRestoreDrillOperations(&RealOperations{AWS: platformaws.Client{Runner: runner}})
	err := operations.verifyCleanupTargetIdentity(context.Background(), RestoreDrillState{
		DrillID: "o3-drill-1", SourceID: "source", TargetID: "target",
		TargetARN: "arn:target", TargetResourceID: "db-target",
		Tags: map[string]string{"Purpose": "restore-drill", "DrillID": "o3-drill-1"},
	}, platformaws.RDSInstance{
		Identifier: "target", ARN: "arn:target", ResourceID: "db-target", Status: "failed",
	})
	require.NoError(t, err)
	require.NoError(t, runner.Verify())
}

func TestRestoreDrillDeleteAttemptsOwnedFailedTarget(t *testing.T) {
	t.Parallel()
	runner := &command.FakeRunner{Expectations: []command.Expectation{
		{
			Name: "aws", Args: []string{"rds", "describe-db-instances",
				"--query", "length(DBInstances[?DBInstanceIdentifier=='target'])", "--output", "text"},
			Result: command.Result{Stdout: "1"},
		},
		{
			Name: "aws", Args: []string{"rds", "describe-db-instances", "--db-instance-identifier", "target", "--output", "json"},
			Result: command.Result{Stdout: `{"DBInstances":[{"DBInstanceIdentifier":"target","DBInstanceArn":"arn:target","DbiResourceId":"db-target","DBInstanceStatus":"failed"}]}`},
		},
		{
			Name: "aws", Args: []string{"rds", "list-tags-for-resource", "--resource-name", "arn:target", "--output", "json"},
			Result: command.Result{Stdout: `{"TagList":[{"Key":"Purpose","Value":"restore-drill"},{"Key":"DrillID","Value":"o3-drill-1"}]}`},
		},
		{
			Name: "aws", Args: []string{"rds", "delete-db-instance", "--db-instance-identifier", "target",
				"--skip-final-snapshot", "--delete-automated-backups"},
		},
	}}
	base := &RealOperations{
		Config: platformconfig.Prod{AccountID: "123456789012", Region: "ap-southeast-1"},
		AWS:    platformaws.Client{Runner: runner},
	}
	err := NewRealRestoreDrillOperations(base).DeleteTarget(context.Background(), RestoreDrillState{
		DrillID: "o3-drill-1", AccountID: "123456789012", Region: "ap-southeast-1",
		SourceID: "source", TargetID: "target", TargetARN: "arn:target", TargetResourceID: "db-target",
		Tags: map[string]string{"Purpose": "restore-drill", "DrillID": "o3-drill-1"},
	})
	require.NoError(t, err)
	require.NoError(t, runner.Verify())
}
