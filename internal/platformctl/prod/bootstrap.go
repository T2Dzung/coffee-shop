package prod

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/thangchung/go-coffeeshop/internal/platformctl/command"
	platformterraform "github.com/thangchung/go-coffeeshop/internal/platformctl/terraform"
)

func (o *RealOperations) Bootstrap(ctx context.Context) error {
	bucketExists := o.awsSucceeds(ctx, "s3api", "head-bucket", "--bucket", o.Config.StateBucket)
	stateExists := bucketExists && o.awsSucceeds(ctx, "s3api", "head-object",
		"--bucket", o.Config.StateBucket, "--key", o.Config.BootstrapStateKey)

	if bucketExists && !stateExists {
		recovery := os.Getenv("PROD_BOOTSTRAP_RECOVERY_STATE")
		if recovery == "" {
			return fmt.Errorf("state bucket exists but %s is absent; explicitly select a reviewed recovery state",
				o.Config.BootstrapStateKey)
		}
		if err := o.initRemote(ctx, o.BootstrapTF, o.Config.BootstrapStateKey); err != nil {
			return err
		}
		if _, err := o.Runner.Run(ctx, command.Request{
			Name:    "terraform",
			Args:    []string{"-chdir=" + o.BootstrapTF.Dir, "state", "push", recovery},
			Env:     map[string]string{"TF_DATA_DIR": o.BootstrapTF.DataDir},
			Timeout: 20 * time.Minute,
			Stream:  true,
		}); err != nil {
			return err
		}
		stateExists = true
	}

	if !bucketExists {
		if err := o.firstBootstrap(ctx); err != nil {
			return err
		}
	} else if stateExists {
		if err := o.initRemote(ctx, o.BootstrapTF, o.Config.BootstrapStateKey); err != nil {
			return err
		}
		if err := o.verifyBootstrapClean(ctx); err != nil {
			return err
		}
	}
	return o.initRemote(ctx, o.FoundationTF, o.Config.FoundationStateKey)
}

func (o *RealOperations) initRemote(
	ctx context.Context,
	client platformterraform.Client,
	key string,
) error {
	kmsARN := os.Getenv("PROD_STATE_KMS_KEY_ID")
	if kmsARN == "" {
		var err error
		kmsARN, err = o.AWS.Text(ctx,
			"kms", "describe-key",
			"--key-id", "alias/"+o.Config.ProjectName+"-state-key",
			"--query", "KeyMetadata.Arn",
			"--output", "text",
		)
		if err != nil {
			return err
		}
	}
	return client.Init(ctx,
		"-backend-config=bucket="+o.Config.StateBucket,
		"-backend-config=key="+key,
		"-backend-config=region="+o.Config.Region,
		"-backend-config=encrypt=true",
		"-backend-config=kms_key_id="+kmsARN,
		"-backend-config=use_lockfile=true",
		"-backend-config=role_arn="+o.Config.BackendRoleARN,
	)
}

func (o *RealOperations) verifyBootstrapClean(ctx context.Context) error {
	artifact, err := o.BootstrapTF.CreatePlan(ctx, "", "prod-bootstrap", false, nil)
	if err != nil {
		return err
	}
	defer artifact.Cleanup()
	if err := o.Policy.Terraform(ctx, "reconcile", artifact.JSONPath); err != nil {
		return err
	}
	if artifact.Summary == (platformterraform.Summary{}) {
		return nil
	}
	return fmt.Errorf(
		"retained backend configuration has drift (%+v); setup will not mutate account-level state before the operation approval",
		artifact.Summary,
	)
}

func (o *RealOperations) firstBootstrap(ctx context.Context) error {
	source := o.BootstrapTF.Dir
	staging, err := os.MkdirTemp(filepath.Dir(source), ".prod-staging-")
	if err != nil {
		return err
	}
	preserve := true
	defer func() {
		if !preserve {
			_ = os.RemoveAll(staging)
		}
	}()
	entries, err := os.ReadDir(source)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".tf" ||
			entry.Name() == "backend.tf" || entry.Name() == "moved.tf" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(source, entry.Name()))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(staging, entry.Name()), data, 0o600); err != nil {
			return err
		}
	}
	client := platformterraform.Client{
		Runner: o.Runner, Dir: staging, DataDir: filepath.Join(staging, ".terraform"),
		Variables: map[string]string{
			"aws_region": o.Config.Region, "expected_aws_account_id": o.Config.AccountID,
			"project_name": o.Config.ProjectName,
		},
		Timeout: 45 * time.Minute,
	}
	if err := client.Init(ctx); err != nil {
		return fmt.Errorf("initialize first bootstrap staging %s: %w", staging, err)
	}
	artifact, err := client.CreatePlan(ctx, "", "first-bootstrap", false, nil)
	if err != nil {
		return err
	}
	defer artifact.Cleanup()
	if err := o.Policy.Terraform(ctx, "reconcile", artifact.JSONPath); err != nil {
		return err
	}
	human, err := client.ShowHuman(ctx, artifact)
	if err != nil {
		return err
	}
	if o.Approver == nil {
		return fmt.Errorf("first bootstrap approval is required; staging retained at %s", staging)
	}
	if err := o.Approver.Approve(ctx, Action("bootstrap"), Plan{
		Artifact: artifact,
		Human:    human,
	}); err != nil {
		return err
	}
	if err := client.Apply(ctx, artifact); err != nil {
		return fmt.Errorf("first bootstrap failed; staging retained at %s: %w", staging, err)
	}
	state := filepath.Join(staging, "terraform.tfstate")
	if err := o.initRemote(ctx, o.BootstrapTF, o.Config.BootstrapStateKey); err != nil {
		return fmt.Errorf("remote bootstrap init failed; staging retained at %s: %w", staging, err)
	}
	if _, err := o.Runner.Run(ctx, command.Request{
		Name:    "terraform",
		Args:    []string{"-chdir=" + o.BootstrapTF.Dir, "state", "push", state},
		Env:     map[string]string{"TF_DATA_DIR": o.BootstrapTF.DataDir},
		Timeout: 20 * time.Minute,
		Stream:  true,
	}); err != nil {
		return fmt.Errorf("state push failed; staging retained at %s: %w", staging, err)
	}
	preserve = false
	return nil
}
