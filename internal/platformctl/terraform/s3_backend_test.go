package terraform

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRenderS3BackendConfigUsesNestedAssumeRole(t *testing.T) {
	t.Parallel()
	content, err := renderS3BackendConfig(S3BackendConfig{
		Bucket: "state", Key: "ci/terraform.tfstate", Region: "ap-southeast-1",
		KMSKeyARN: "arn:aws:kms:ap-southeast-1:123456789012:key/test",
		RoleARN:   "arn:aws:iam::123456789012:role/state", Encrypt: true, UseLockfile: true,
	})
	require.NoError(t, err)
	require.Contains(t, content, "assume_role = {")
	require.Contains(t, content, `role_arn = "arn:aws:iam::123456789012:role/state"`)
	require.NotContains(t, content, "\nrole_arn =")
	require.Equal(t, 1, strings.Count(content, "role_arn"))
}

func TestRenderS3BackendConfigRejectsIncompleteInput(t *testing.T) {
	t.Parallel()
	_, err := renderS3BackendConfig(S3BackendConfig{Bucket: "state"})
	require.ErrorContains(t, err, "bucket, key, region and KMS key ARN are required")
}
