package prod

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	platformaws "github.com/thangchung/go-coffeeshop/internal/platformctl/aws"
)

const (
	restoreDrillNamespace = "coffeeshop"
	restoreDrillDatabase  = "postgres"
	restoreDrillUser      = "coffeeshop_app"
)

type RealRestoreDrillOperations struct {
	Base  *RealOperations
	Clock func() time.Time
}

func NewRealRestoreDrillOperations(base *RealOperations) *RealRestoreDrillOperations {
	return &RealRestoreDrillOperations{Base: base}
}

func (o *RealRestoreDrillOperations) Prepare(ctx context.Context) (RestoreDrillPrepared, error) {
	if o.Base == nil {
		return RestoreDrillPrepared{}, errors.New("PROD operations are required")
	}
	if err := o.Base.Preflight(ctx, ActionStatus); err != nil {
		return RestoreDrillPrepared{}, err
	}
	if err := o.Base.initRemote(ctx, o.Base.FoundationTF, o.Base.Config.FoundationStateKey); err != nil {
		return RestoreDrillPrepared{}, err
	}
	if err := o.Base.loadRuntimeOutputs(ctx); err != nil {
		return RestoreDrillPrepared{}, err
	}
	if err := o.Base.updateKubeconfig(ctx); err != nil {
		return RestoreDrillPrepared{}, err
	}
	if err := o.Base.Verify(ctx, ActionStatus); err != nil {
		return RestoreDrillPrepared{}, fmt.Errorf("PROD runtime preflight: %w", err)
	}
	sourceID := o.Base.Config.ProjectName + "-" + o.Base.Config.Environment + "-db"
	source, err := o.Base.AWS.DescribeRDSInstance(ctx, sourceID)
	if err != nil {
		return RestoreDrillPrepared{}, err
	}
	if err := validateRestoreDrillSource(source, sourceID); err != nil {
		return RestoreDrillPrepared{}, err
	}
	window, err := o.Base.AWS.DescribeRDSRestoreWindow(ctx, sourceID)
	if err != nil {
		return RestoreDrillPrepared{}, err
	}
	if err := validateRestoreWindow(window); err != nil {
		return RestoreDrillPrepared{}, err
	}
	existing, err := o.Base.AWS.FindTaggedRDSResources(ctx, map[string]string{"Purpose": "restore-drill"})
	if err != nil {
		return RestoreDrillPrepared{}, err
	}
	if len(existing) != 0 {
		return RestoreDrillPrepared{}, fmt.Errorf("another tagged restore drill target exists: %s", existing[0].ARN)
	}
	image, err := o.Base.migrationImage()
	if err != nil {
		return RestoreDrillPrepared{}, err
	}
	secretHash, err := o.applicationSecretHash(ctx)
	if err != nil {
		return RestoreDrillPrepared{}, err
	}
	suffix, err := randomHex(4)
	if err != nil {
		return RestoreDrillPrepared{}, err
	}
	drillID := "o3-" + suffix
	now := o.now()
	targetID := fmt.Sprintf("%s-pitr-%s-%s", sourceID, now.Format("20060102t150405"), suffix)
	tags := map[string]string{
		"Project": o.Base.Config.ProjectName, "Environment": o.Base.Config.Environment,
		"Owner": "PlatformEngineering", "ManagedBy": "platformctl", "Purpose": "restore-drill",
		"DrillID": drillID, "ExpiresAt": now.Add(6 * time.Hour).Format(time.RFC3339),
	}
	prepared := RestoreDrillPrepared{
		DrillID: drillID, AccountID: o.Base.Config.AccountID, Region: o.Base.Config.Region,
		SourceID: source.Identifier, SourceARN: source.ARN, SourceResourceID: source.ResourceID,
		SourceEndpoint:      source.Endpoint,
		SourceInstanceClass: source.InstanceClass, SourceAllocatedStorage: source.AllocatedStorageGiB,
		SourceSubnetGroup: source.SubnetGroup, SourceSecurityGroups: append([]string(nil), source.SecurityGroupIDs...),
		SourcePort: source.Port, TargetID: targetID, TargetInstanceClass: o.Base.Config.RDSInstanceClass,
		MigrationImage: image, ApplicationSecretHash: secretHash,
		MarkerPayload: "restore-drill-" + suffix, Tags: tags,
	}
	probeState := RestoreDrillState{
		DrillID: drillID, MigrationImage: image, MarkerPayload: prepared.MarkerPayload,
		SourceID: sourceID, SourcePort: source.Port,
	}
	if _, err := o.runJob(ctx, probeState, "probe", "", true); err != nil {
		return RestoreDrillPrepared{}, fmt.Errorf("source app-role TLS probe: %w", err)
	}
	fmt.Fprintf(o.Base.Output, `Restore drill preview:
  source/target : %s -> %s
  account/Region: %s / %s
  network       : subnet group %s; RDS security groups %s; private
  target class  : %s; Single-AZ; backup retention 0
  source window : %s .. %s
  expires       : %s
Cost warning: the temporary RDS instance and storage accrue charges until cleanup completes.
`, sourceID, targetID, o.Base.Config.AccountID, o.Base.Config.Region, source.SubnetGroup,
		strings.Join(source.SecurityGroupIDs, ","), o.Base.Config.RDSInstanceClass,
		window.EarliestTime.UTC().Format(time.RFC3339), window.LatestTime.UTC().Format(time.RFC3339), tags["ExpiresAt"])
	return prepared, nil
}

func (o *RealRestoreDrillOperations) WriteMarkerA(ctx context.Context, state RestoreDrillState) (RestoreDrillMarkerA, error) {
	result, err := o.runJob(ctx, state, "write-a", "", true)
	if err != nil {
		return RestoreDrillMarkerA{}, err
	}
	if result.RestoreTime.IsZero() || result.Checksum == "" {
		return RestoreDrillMarkerA{}, errors.New("marker A job omitted restore time or checksum")
	}
	return RestoreDrillMarkerA{RestoreTime: result.RestoreTime, Checksum: result.Checksum}, nil
}

func (o *RealRestoreDrillOperations) WaitRestoreWindow(ctx context.Context, state RestoreDrillState) (RestoreDrillWindow, error) {
	var observed platformaws.RDSRestoreWindow
	err := o.wait(ctx, o.Base.Config.PollAttempts, "RDS restore window to pass marker A", func(ctx context.Context) (bool, error) {
		var err error
		observed, err = o.Base.AWS.DescribeRDSRestoreWindow(ctx, state.SourceID)
		if err != nil {
			return false, nil
		}
		if err := validateRestoreWindow(observed); err != nil {
			return false, err
		}
		return !state.RestoreTime.Before(observed.EarliestTime) && state.RestoreTime.Before(observed.LatestTime), nil
	})
	if err != nil {
		return RestoreDrillWindow{}, err
	}
	return RestoreDrillWindow{Earliest: observed.EarliestTime, Latest: observed.LatestTime}, nil
}

func (o *RealRestoreDrillOperations) WriteMarkerB(ctx context.Context, state RestoreDrillState) error {
	_, err := o.runJob(ctx, state, "write-b", "", true)
	return err
}

func (o *RealRestoreDrillOperations) RequestRestore(ctx context.Context, state RestoreDrillState) (RestoreDrillTarget, error) {
	source, err := o.checkedSource(ctx, state)
	if err != nil {
		return RestoreDrillTarget{}, err
	}
	window, err := o.Base.AWS.DescribeRDSRestoreWindow(ctx, state.SourceID)
	if err != nil {
		return RestoreDrillTarget{}, err
	}
	if state.RestoreTime.Before(window.EarliestTime) || !state.RestoreTime.Before(window.LatestTime) {
		return RestoreDrillTarget{}, fmt.Errorf("restore time %s is outside current window %s..%s",
			state.RestoreTime.UTC().Format(time.RFC3339Nano), window.EarliestTime.UTC().Format(time.RFC3339Nano),
			window.LatestTime.UTC().Format(time.RFC3339Nano))
	}
	count, err := o.rdsCount(ctx, state.TargetID)
	if err != nil {
		return RestoreDrillTarget{}, err
	}
	if count == 1 {
		existing, err := o.Base.AWS.DescribeRDSInstance(ctx, state.TargetID)
		if err != nil {
			return RestoreDrillTarget{}, err
		}
		if err := o.verifyTargetIdentity(ctx, state, existing); err != nil {
			return RestoreDrillTarget{}, err
		}
		return restoreTarget(existing), nil
	}
	owned, err := o.Base.AWS.FindTaggedRDSResources(ctx, map[string]string{"Purpose": "restore-drill"})
	if err != nil {
		return RestoreDrillTarget{}, err
	}
	if len(owned) != 0 {
		return RestoreDrillTarget{}, fmt.Errorf("refusing target creation while tagged restore target exists: %s", owned[0].ARN)
	}
	instance, err := o.Base.AWS.RestoreRDSPointInTime(ctx, platformaws.RDSPointInTimeRestoreRequest{
		SourceIdentifier: state.SourceID, TargetIdentifier: state.TargetID, RestoreTime: state.RestoreTime,
		InstanceClass: state.TargetInstanceClass, SubnetGroup: source.SubnetGroup,
		SecurityGroupIDs: append([]string(nil), source.SecurityGroupIDs...), Port: source.Port,
		BackupRetentionDays: 0, PubliclyAccessible: false, MultiAZ: false, DeletionProtection: false,
		Tags: cloneStrings(state.Tags),
	})
	if err != nil {
		return RestoreDrillTarget{}, err
	}
	return restoreTarget(instance), nil
}

func (o *RealRestoreDrillOperations) WaitTargetAvailable(ctx context.Context, state RestoreDrillState) (RestoreDrillTarget, error) {
	var instance platformaws.RDSInstance
	err := o.wait(ctx, o.Base.Config.ReleaseAttempts, "RDS PITR target available", func(ctx context.Context) (bool, error) {
		var err error
		instance, err = o.Base.AWS.DescribeRDSInstance(ctx, state.TargetID)
		if err != nil {
			return false, nil
		}
		switch instance.Status {
		case "available":
			if err := o.verifyTargetIdentity(ctx, state, instance); err != nil {
				return false, err
			}
			return true, nil
		case "creating", "backing-up", "configuring-enhanced-monitoring", "modifying", "rebooting":
			return false, nil
		default:
			return false, fmt.Errorf("RDS target entered terminal/unexpected status %q", instance.Status)
		}
	})
	if err != nil {
		return RestoreDrillTarget{}, err
	}
	return restoreTarget(instance), nil
}

func (o *RealRestoreDrillOperations) ValidateTarget(ctx context.Context, state RestoreDrillState) error {
	if err := o.verifyApplicationSecretHash(ctx, state); err != nil {
		return err
	}
	instance, err := o.Base.AWS.DescribeRDSInstance(ctx, state.TargetID)
	if err != nil {
		return err
	}
	if err := o.verifyTargetIdentity(ctx, state, instance); err != nil {
		return err
	}
	if instance.Endpoint == "" || instance.Endpoint == state.SourceEndpoint || instance.ResourceID == state.SourceResourceID {
		return errors.New("restored target is not isolated from source identity")
	}
	_, err = o.runJob(ctx, state, "validate", instance.Endpoint, false)
	return err
}

func (o *RealRestoreDrillOperations) ReadStatus(ctx context.Context, state RestoreDrillState) (RestoreDrillStatus, error) {
	if err := o.Base.Preflight(ctx, ActionStatus); err != nil {
		return RestoreDrillStatus{}, err
	}
	source, err := o.Base.AWS.DescribeRDSInstance(ctx, state.SourceID)
	if err != nil {
		return RestoreDrillStatus{}, err
	}
	count, err := o.rdsCount(ctx, state.TargetID)
	if err != nil {
		return RestoreDrillStatus{}, err
	}
	status := RestoreDrillStatus{SourceStatus: source.Status, TargetExists: count == 1}
	if count == 1 {
		target, err := o.Base.AWS.DescribeRDSInstance(ctx, state.TargetID)
		if err != nil {
			return status, err
		}
		status.TargetStatus = target.Status
	}
	return status, nil
}

func (o *RealRestoreDrillOperations) PrepareCleanup(ctx context.Context, state RestoreDrillState) error {
	if err := o.Base.Preflight(ctx, ActionStatus); err != nil {
		return err
	}
	if err := o.validateCleanupIdentity(state); err != nil {
		return err
	}
	count, err := o.rdsCount(ctx, state.TargetID)
	if err != nil {
		return err
	}
	if count == 0 {
		return nil
	}
	target, err := o.Base.AWS.DescribeRDSInstance(ctx, state.TargetID)
	if err != nil {
		return err
	}
	return o.verifyCleanupTargetIdentity(ctx, state, target)
}

func (o *RealRestoreDrillOperations) DeleteTarget(ctx context.Context, state RestoreDrillState) error {
	if err := o.validateCleanupIdentity(state); err != nil {
		return err
	}
	count, err := o.rdsCount(ctx, state.TargetID)
	if err != nil {
		return err
	}
	if count == 0 {
		return nil
	}
	target, err := o.Base.AWS.DescribeRDSInstance(ctx, state.TargetID)
	if err != nil {
		return err
	}
	if err := o.verifyCleanupTargetIdentity(ctx, state, target); err != nil {
		return err
	}
	if target.Status == "deleting" {
		return nil
	}
	return o.Base.AWS.DeleteRDSInstance(ctx, state.TargetID)
}

func (o *RealRestoreDrillOperations) WaitTargetDeleted(ctx context.Context, state RestoreDrillState) error {
	return o.wait(ctx, o.Base.Config.ReleaseAttempts, "RDS restore target deleted", func(ctx context.Context) (bool, error) {
		count, err := o.rdsCount(ctx, state.TargetID)
		return count == 0, err
	})
}

func (o *RealRestoreDrillOperations) CleanupSourceMarker(ctx context.Context, state RestoreDrillState) error {
	if _, err := o.runJob(ctx, state, "cleanup", "", true); err != nil {
		return err
	}
	_, err := o.Base.Kube.Kubectl(ctx, nil, "delete", "job", "-n", restoreDrillNamespace,
		"-l", "platform.coffeeshop.dev/restore-drill-id="+state.DrillID, "--ignore-not-found", "--wait=true")
	return err
}

func (o *RealRestoreDrillOperations) VerifyClean(ctx context.Context, state RestoreDrillState) error {
	if count, err := o.rdsCount(ctx, state.TargetID); err != nil || count != 0 {
		return fmt.Errorf("restore target inventory after cleanup: count=%d: %w", count, err)
	}
	orphans, err := o.Base.AWS.FindTaggedRDSResources(ctx, map[string]string{"Purpose": "restore-drill", "DrillID": state.DrillID})
	if err != nil {
		return err
	}
	if len(orphans) != 0 {
		return fmt.Errorf("tagged restore drill RDS orphan remains: %s", orphans[0].ARN)
	}
	source, err := o.checkedSource(ctx, state)
	if err != nil {
		return err
	}
	window, err := o.Base.AWS.DescribeRDSRestoreWindow(ctx, source.Identifier)
	if err != nil {
		return err
	}
	if err := validateRestoreWindow(window); err != nil {
		return err
	}
	if err := o.verifyApplicationSecretHash(ctx, state); err != nil {
		return err
	}
	snapshots, err := o.Base.AWS.Text(ctx, "rds", "describe-db-snapshots",
		"--query", "length(DBSnapshots[?DBInstanceIdentifier=='"+state.TargetID+"'])", "--output", "text")
	if err != nil || snapshots != "0" {
		return fmt.Errorf("restore target snapshot inventory is %q: %w", snapshots, err)
	}
	if state.TargetMasterSecretARN != "" {
		secrets, err := o.Base.AWS.Text(ctx, "secretsmanager", "list-secrets", "--include-planned-deletion",
			"--query", "length(SecretList[?ARN=='"+state.TargetMasterSecretARN+"'])", "--output", "text")
		if err != nil || secrets != "0" {
			return fmt.Errorf("restore target managed Secret inventory is %q: %w", secrets, err)
		}
	}
	jobs, err := o.Base.Kube.Kubectl(ctx, nil, "get", "jobs", "-n", restoreDrillNamespace,
		"-l", "platform.coffeeshop.dev/restore-drill-id="+state.DrillID, "-o", "jsonpath={.items[*].metadata.name}")
	if err != nil {
		return err
	}
	if strings.TrimSpace(jobs) != "" {
		return fmt.Errorf("restore drill Jobs remain: %s", jobs)
	}
	return nil
}

func (o *RealRestoreDrillOperations) checkedSource(ctx context.Context, state RestoreDrillState) (platformaws.RDSInstance, error) {
	if state.AccountID != o.Base.Config.AccountID || state.Region != o.Base.Config.Region {
		return platformaws.RDSInstance{}, errors.New("state account/Region does not match current PROD config")
	}
	source, err := o.Base.AWS.DescribeRDSInstance(ctx, state.SourceID)
	if err != nil {
		return source, err
	}
	if err := validateRestoreDrillSource(source, state.SourceID); err != nil {
		return source, err
	}
	if source.ARN != state.SourceARN || source.ResourceID != state.SourceResourceID {
		return source, errors.New("source RDS identity changed since restore drill preflight")
	}
	if source.Endpoint != state.SourceEndpoint {
		return source, errors.New("source RDS endpoint changed since restore drill preflight")
	}
	if source.SubnetGroup != state.SourceSubnetGroup || source.Port != state.SourcePort ||
		strings.Join(source.SecurityGroupIDs, ",") != strings.Join(state.SourceSecurityGroups, ",") {
		return source, errors.New("source RDS network contract changed since restore drill preflight")
	}
	return source, nil
}

func (o *RealRestoreDrillOperations) validateCleanupIdentity(state RestoreDrillState) error {
	if state.AccountID != o.Base.Config.AccountID || state.Region != o.Base.Config.Region {
		return errors.New("state account/Region does not match current PROD config")
	}
	if state.SourceID == "" || state.TargetID == "" || state.SourceID == state.TargetID {
		return errors.New("restore drill cleanup source/target identity is invalid")
	}
	return nil
}

func (o *RealRestoreDrillOperations) verifyCleanupTargetIdentity(ctx context.Context, state RestoreDrillState, target platformaws.RDSInstance) error {
	if target.Identifier != state.TargetID || target.Identifier == state.SourceID {
		return errors.New("target RDS identifier does not match exact state")
	}
	if state.TargetARN != "" && target.ARN != state.TargetARN {
		return errors.New("target RDS ARN changed since restore request")
	}
	if state.TargetResourceID != "" && target.ResourceID != state.TargetResourceID {
		return errors.New("target RDS resource ID changed since restore request")
	}
	tags, err := o.Base.AWS.RDSTags(ctx, target.ARN)
	if err != nil {
		return err
	}
	for _, key := range []string{"Project", "Environment", "ManagedBy", "Purpose", "DrillID"} {
		expected := state.Tags[key]
		if expected != "" && tags[key] != expected {
			return fmt.Errorf("target RDS ownership tag %s does not match state", key)
		}
	}
	if tags["Purpose"] != "restore-drill" || tags["DrillID"] != state.DrillID {
		return errors.New("target RDS ownership tags do not identify this restore drill")
	}
	return nil
}

func (o *RealRestoreDrillOperations) verifyTargetIdentity(ctx context.Context, state RestoreDrillState, target platformaws.RDSInstance) error {
	if target.Identifier != state.TargetID || target.Identifier == state.SourceID {
		return errors.New("target RDS identifier does not match exact state")
	}
	if state.TargetARN != "" && target.ARN != state.TargetARN {
		return errors.New("target RDS ARN changed since restore request")
	}
	if state.TargetResourceID != "" && target.ResourceID != state.TargetResourceID {
		return errors.New("target RDS resource ID changed since restore request")
	}
	if target.PubliclyAccessible || !target.StorageEncrypted || target.MultiAZ || target.BackupRetentionDays != 0 {
		return errors.New("target RDS isolation/retention contract does not match restore drill")
	}
	if target.InstanceClass != state.TargetInstanceClass || target.AllocatedStorageGiB < state.SourceAllocatedStorage {
		return errors.New("target RDS class/storage contract does not match restore drill")
	}
	if target.SubnetGroup != state.SourceSubnetGroup || target.Port != state.SourcePort ||
		strings.Join(target.SecurityGroupIDs, ",") != strings.Join(state.SourceSecurityGroups, ",") {
		return errors.New("target RDS network contract does not match source isolation")
	}
	tags, err := o.Base.AWS.RDSTags(ctx, target.ARN)
	if err != nil {
		return err
	}
	for key, expected := range state.Tags {
		if tags[key] != expected {
			return fmt.Errorf("target RDS tag %s does not match state", key)
		}
	}
	return nil
}

func (o *RealRestoreDrillOperations) rdsCount(ctx context.Context, identifier string) (int, error) {
	value, err := o.Base.AWS.Text(ctx, "rds", "describe-db-instances",
		"--query", "length(DBInstances[?DBInstanceIdentifier=='"+identifier+"'])", "--output", "text")
	if err != nil {
		return 0, err
	}
	count, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("decode RDS count %q: %w", value, err)
	}
	if count < 0 || count > 1 {
		return count, fmt.Errorf("unexpected RDS count %d for %s", count, identifier)
	}
	return count, nil
}

func (o *RealRestoreDrillOperations) applicationSecretHash(ctx context.Context) (string, error) {
	data, err := o.Base.Kube.Kubectl(ctx, nil, "get", "secret", "coffeeshop-secret",
		"-n", restoreDrillNamespace, "-o", "json")
	if err != nil {
		return "", fmt.Errorf("read application Secret identity: %w", err)
	}
	var secret struct {
		Data map[string]string `json:"data"`
	}
	if err := json.Unmarshal([]byte(data), &secret); err != nil {
		return "", fmt.Errorf("decode application Secret identity: %w", err)
	}
	if secret.Data["PG_URL"] == "" || secret.Data["APP_DB_PASSWORD"] == "" {
		return "", errors.New("application Secret lacks PG_URL or APP_DB_PASSWORD")
	}
	digest := sha256.Sum256([]byte(secret.Data["PG_URL"] + "\x00" + secret.Data["APP_DB_PASSWORD"]))
	return hex.EncodeToString(digest[:]), nil
}

func (o *RealRestoreDrillOperations) verifyApplicationSecretHash(ctx context.Context, state RestoreDrillState) error {
	if state.ApplicationSecretHash == "" {
		return errors.New("restore drill state lacks application Secret checksum")
	}
	current, err := o.applicationSecretHash(ctx)
	if err != nil {
		return err
	}
	if current != state.ApplicationSecretHash {
		return errors.New("application credential Secret changed during restore drill")
	}
	return nil
}

func validateRestoreDrillSource(source platformaws.RDSInstance, expectedID string) error {
	if source.Identifier != expectedID || source.Status != "available" || source.Engine != "postgres" {
		return fmt.Errorf("source RDS %s is not the expected available PostgreSQL instance", expectedID)
	}
	if source.PubliclyAccessible || !source.StorageEncrypted || source.BackupRetentionDays < 1 {
		return errors.New("source RDS must be private, encrypted and have backup retention enabled")
	}
	if source.ARN == "" || source.ResourceID == "" || source.SubnetGroup == "" || len(source.SecurityGroupIDs) == 0 || source.Port < 1 {
		return errors.New("source RDS identity/network metadata is incomplete")
	}
	return nil
}

func validateRestoreWindow(window platformaws.RDSRestoreWindow) error {
	if window.Status != "active" || !window.Encrypted || window.Retention < 1 {
		return errors.New("RDS automated backup is not active, encrypted and retained")
	}
	if window.EarliestTime.IsZero() || window.LatestTime.IsZero() || !window.EarliestTime.Before(window.LatestTime) {
		return errors.New("RDS restore window is empty or invalid")
	}
	return nil
}

func restoreTarget(instance platformaws.RDSInstance) RestoreDrillTarget {
	return RestoreDrillTarget{ARN: instance.ARN, ResourceID: instance.ResourceID, Endpoint: instance.Endpoint,
		MasterSecretARN: instance.MasterUserSecretARN}
}

func (o *RealRestoreDrillOperations) now() time.Time {
	if o.Clock != nil {
		return o.Clock().UTC()
	}
	return time.Now().UTC()
}

func (o *RealRestoreDrillOperations) wait(ctx context.Context, attempts int, description string, observe func(context.Context) (bool, error)) error {
	for attempt := 1; attempt <= attempts; attempt++ {
		ready, err := observe(ctx)
		if err != nil {
			return fmt.Errorf("%s: %w", description, err)
		}
		if ready {
			return nil
		}
		if attempt == attempts {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Second):
		}
	}
	return fmt.Errorf("%s did not complete after %d attempts", description, attempts)
}
