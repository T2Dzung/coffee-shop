package release

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	testCommit = "0123456789abcdef0123456789abcdef01234567"
	testDigest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	testImage  = "123456789012.dkr.ecr.ap-southeast-1.amazonaws.com/candidate/web"
)

func TestValidateStandard(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	candidate := filepath.Join(dir, "candidate.json")
	qa := filepath.Join(dir, "qa.json")
	require.NoError(t, os.WriteFile(candidate, []byte(`{
		"schema_version":1,"status":"built","service":"web",
		"source_commit":"`+testCommit+`","source_image":"`+testImage+`","source_digest":"`+testDigest+`"
	}`), 0o600))
	require.NoError(t, os.WriteFile(qa, []byte(`{
		"schema_version":3,"qa_status":"approved","service":"web",
		"source_commit":"`+testCommit+`","source_image":"`+testImage+`",
		"source_digest":"`+testDigest+`","dev_image":"dev-runtime/web",
		"dev_digest":"`+testDigest+`","evidence_url":"https://example.test/qa",
		"dev_evidence_url":"https://example.test/dev"
	}`), 0o600))

	artifact, err := ValidateStandard("web", testCommit, candidate, qa)
	require.NoError(t, err)
	require.Equal(t, testDigest, artifact.Digest)
}

func TestValidateStandardRejectsDigestMismatch(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	candidate := filepath.Join(dir, "candidate.json")
	qa := filepath.Join(dir, "qa.json")
	require.NoError(t, os.WriteFile(candidate, []byte(`{
		"schema_version":1,"status":"built","service":"web",
		"source_commit":"`+testCommit+`","source_image":"`+testImage+`","source_digest":"`+testDigest+`"
	}`), 0o600))
	require.NoError(t, os.WriteFile(qa, []byte(`{
		"schema_version":3,"qa_status":"approved","service":"web",
		"source_commit":"`+testCommit+`","source_image":"`+testImage+`",
		"source_digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"dev_image":"dev-runtime/web","dev_digest":"`+testDigest+`",
		"evidence_url":"https://example.test/qa","dev_evidence_url":"https://example.test/dev"
	}`), 0o600))

	_, err := ValidateStandard("web", testCommit, candidate, qa)
	require.ErrorContains(t, err, "exact candidate")
}

func TestValidateDevReleaseRequiresExactCandidateDigest(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	candidate := filepath.Join(dir, "candidate.json")
	devRelease := filepath.Join(dir, "dev.json")
	require.NoError(t, os.WriteFile(candidate, []byte(`{
		"schema_version":2,"status":"built","service":"web",
		"source_commit":"`+testCommit+`","source_image":"`+testImage+`","source_digest":"`+testDigest+`"
	}`), 0o600))
	require.NoError(t, os.WriteFile(devRelease, []byte(`{
		"schema_version":1,"status":"copied","service":"web",
		"source_commit":"`+testCommit+`","source_image":"`+testImage+`",
		"source_digest":"`+testDigest+`","dev_image":"dev/web","dev_digest":"`+testDigest+`",
		"workflow_run":"https://example.test/run/1"
	}`), 0o600))
	release, err := ValidateDevRelease("web", testCommit, candidate, devRelease)
	require.NoError(t, err)
	require.Equal(t, testDigest, release.DevDigest)

	require.NoError(t, os.WriteFile(devRelease, []byte(`{
		"schema_version":1,"status":"copied","service":"web",
		"source_commit":"`+testCommit+`","source_image":"`+testImage+`",
		"source_digest":"`+testDigest+`","dev_image":"dev/web",
		"dev_digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"workflow_run":"https://example.test/run/1"
	}`), 0o600))
	_, err = ValidateDevRelease("web", testCommit, candidate, devRelease)
	require.ErrorContains(t, err, "exact copy")
}

func TestValidateCandidateAcceptsCurrentSchema(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "candidate.json")
	require.NoError(t, os.WriteFile(path, []byte(`{
		"schema_version":2,"status":"built","service":"web",
		"source_commit":"`+testCommit+`","source_image":"`+testImage+`","source_digest":"`+testDigest+`"
	}`), 0o600))
	artifact, err := ValidateCandidate("web", testCommit, path)
	require.NoError(t, err)
	require.Equal(t, testImage, artifact.Image)
	require.Equal(t, "ap-southeast-1", artifact.Region)
}

func TestValidateCandidateRejectsNonECRSource(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "candidate.json")
	require.NoError(t, os.WriteFile(path, []byte(`{
		"schema_version":2,"status":"built","service":"web",
		"source_commit":"`+testCommit+`","source_image":"candidate/web","source_digest":"`+testDigest+`"
	}`), 0o600))

	_, err := ValidateCandidate("web", testCommit, path)
	require.ErrorContains(t, err, "Amazon ECR")
}

func TestValidateRequest(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "request.json")
	require.NoError(t, os.WriteFile(path, []byte(`{
		"schema_version":1,"status":"requested","source_commit":"`+testCommit+`",
		"components":["web","platform-ownership-guard"]
	}`), 0o600))

	request, err := ValidateRequest(path)
	require.NoError(t, err)
	require.Equal(t, testCommit, request.SourceCommit)
	require.Equal(t, []string{"web", "platform-ownership-guard"}, request.Components)
}

func TestValidateRequestRejectsDuplicateComponent(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "request.json")
	require.NoError(t, os.WriteFile(path, []byte(`{
		"schema_version":1,"status":"requested","source_commit":"`+testCommit+`",
		"components":["web","web"]
	}`), 0o600))

	_, err := ValidateRequest(path)
	require.ErrorContains(t, err, "duplicate")
}

func TestValidateRollback(t *testing.T) {
	t.Parallel()
	history := filepath.Join(t.TempDir(), "release.json")
	require.NoError(t, os.WriteFile(history, []byte(`{
		"schema_version":1,"service":"web","source_commit":"`+testCommit+`",
		"prod_image":"prod/web","prod_digest":"`+testDigest+`"
	}`), 0o600))

	artifact, err := ValidateRollback("web", testCommit, history)
	require.NoError(t, err)
	require.Equal(t, "prod/web", artifact.Image)
}

func TestNewManifest(t *testing.T) {
	t.Parallel()
	manifest, err := NewManifest(
		"emergency", "web", testCommit, "prod/web", testDigest, testCommit,
		"https://example.test/run/1", "2026-07-25T00:00:00Z",
	)
	require.NoError(t, err)
	require.Equal(t, 2, manifest.SchemaVersion)
	require.Equal(t, "emergency", manifest.Lane)
}

func TestValidateIdentityRejectsUnsupportedInput(t *testing.T) {
	t.Parallel()
	require.ErrorContains(t, ValidateIdentity("preview", "web", testCommit), "lane")
	require.ErrorContains(t, ValidateIdentity("standard", "UPPER", testCommit), "component")
	require.ErrorContains(t, ValidateIdentity("standard", "web", "short"), "40-character")
}
