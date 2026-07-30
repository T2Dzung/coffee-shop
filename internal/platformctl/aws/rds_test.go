package aws

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/thangchung/go-coffeeshop/internal/platformctl/command"
)

func TestDescribeRDSInstanceDecodesRuntimeIdentity(t *testing.T) {
	t.Parallel()
	runner := &command.FakeRunner{Expectations: []command.Expectation{{
		Name: "aws", Args: []string{"rds", "describe-db-instances", "--db-instance-identifier", "source", "--output", "json"},
		Result: command.Result{Stdout: `{"DBInstances":[{
          "DBInstanceIdentifier":"source","DBInstanceArn":"arn:source","DbiResourceId":"db-1",
          "DBInstanceStatus":"available","Engine":"postgres","EngineVersion":"16.14",
          "DBInstanceClass":"db.t4g.micro","AllocatedStorage":20,"StorageType":"gp3","Port":5432,
          "BackupRetentionPeriod":7,"PubliclyAccessible":false,"StorageEncrypted":true,
          "MultiAZ":false,"DeletionProtection":false,"LatestRestorableTime":"2026-07-30T05:14:00Z",
          "Endpoint":{"Address":"source.internal","Port":5432},
          "DBSubnetGroup":{"DBSubnetGroupName":"private","VpcId":"vpc-1"},
          "VpcSecurityGroups":[{"VpcSecurityGroupId":"sg-2"},{"VpcSecurityGroupId":"sg-1"}],
          "MasterUserSecret":{"SecretArn":"arn:secret","SecretStatus":"active"}
        }]}`},
	}}}
	instance, err := (Client{Runner: runner}).DescribeRDSInstance(context.Background(), "source")
	require.NoError(t, err)
	require.Equal(t, "source.internal", instance.Endpoint)
	require.Equal(t, []string{"sg-1", "sg-2"}, instance.SecurityGroupIDs)
	require.True(t, instance.StorageEncrypted)
	require.NoError(t, runner.Verify())
}

func TestRestoreRDSPointInTimeUsesExplicitIsolationAndSortedTags(t *testing.T) {
	t.Parallel()
	restoreTime := time.Date(2026, 7, 30, 5, 0, 0, 123000000, time.UTC)
	wantArgs := []string{
		"rds", "restore-db-instance-to-point-in-time",
		"--source-db-instance-identifier", "source",
		"--target-db-instance-identifier", "target",
		"--restore-time", "2026-07-30T05:00:00.123Z",
		"--db-instance-class", "db.t4g.micro",
		"--db-subnet-group-name", "private",
		"--vpc-security-group-ids", "sg-1",
		"--port", "5432", "--backup-retention-period", "0",
		"--no-publicly-accessible", "--no-multi-az", "--no-deletion-protection",
		"--tags", "Key=DrillID,Value=o3-1", "Key=Purpose,Value=restore-drill",
		"--output", "json",
	}
	runner := &command.FakeRunner{Expectations: []command.Expectation{{
		Name: "aws", Args: wantArgs,
		Result: command.Result{Stdout: `{"DBInstance":{"DBInstanceIdentifier":"target","DBInstanceArn":"arn:target","DbiResourceId":"db-2","DBInstanceStatus":"creating"}}`},
	}}}
	instance, err := (Client{Runner: runner}).RestoreRDSPointInTime(context.Background(), RDSPointInTimeRestoreRequest{
		SourceIdentifier: "source", TargetIdentifier: "target", RestoreTime: restoreTime,
		InstanceClass: "db.t4g.micro", SubnetGroup: "private", SecurityGroupIDs: []string{"sg-1"},
		Port: 5432, Tags: map[string]string{"Purpose": "restore-drill", "DrillID": "o3-1"},
	})
	require.NoError(t, err)
	require.Equal(t, "target", instance.Identifier)
	require.NoError(t, runner.Verify())
}

func TestRestoreRDSPointInTimeRejectsSourceAsTargetBeforeMutation(t *testing.T) {
	t.Parallel()
	runner := &command.FakeRunner{}
	_, err := (Client{Runner: runner}).RestoreRDSPointInTime(context.Background(), RDSPointInTimeRestoreRequest{
		SourceIdentifier: "same", TargetIdentifier: "same", RestoreTime: time.Now(),
		InstanceClass: "db.t4g.micro", SubnetGroup: "private", SecurityGroupIDs: []string{"sg-1"},
	})
	require.ErrorContains(t, err, "must differ")
	require.Empty(t, runner.Requests)
}

func TestDeleteRDSInstanceUsesOneLiteralIdentifier(t *testing.T) {
	t.Parallel()
	runner := &command.FakeRunner{Expectations: []command.Expectation{{
		Name: "aws", Args: []string{"rds", "delete-db-instance", "--db-instance-identifier", "target",
			"--skip-final-snapshot", "--delete-automated-backups"},
	}}}
	require.NoError(t, (Client{Runner: runner}).DeleteRDSInstance(context.Background(), "target"))
	require.NoError(t, runner.Verify())
}

func TestFindTaggedRDSResourcesUsesSortedSemanticFilters(t *testing.T) {
	t.Parallel()
	runner := &command.FakeRunner{Expectations: []command.Expectation{{
		Name: "aws", Args: []string{"resourcegroupstaggingapi", "get-resources",
			"--resource-type-filters", "rds:db", "--tag-filters",
			"Key=DrillID,Values=o3-1", "Key=Purpose,Values=restore-drill", "--output", "json"},
		Result: command.Result{Stdout: `{"ResourceTagMappingList":[{"ResourceARN":"arn:target","Tags":[{"Key":"Purpose","Value":"restore-drill"},{"Key":"DrillID","Value":"o3-1"}]}]}`},
	}}}
	resources, err := (Client{Runner: runner}).FindTaggedRDSResources(context.Background(), map[string]string{
		"Purpose": "restore-drill", "DrillID": "o3-1",
	})
	require.NoError(t, err)
	require.Equal(t, "arn:target", resources[0].ARN)
	require.Equal(t, "o3-1", resources[0].Tags["DrillID"])
	require.NoError(t, runner.Verify())
}
