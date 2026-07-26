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
)

func TestValidateStandard(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	candidate := filepath.Join(dir, "candidate.json")
	qa := filepath.Join(dir, "qa.json")
	require.NoError(t, os.WriteFile(candidate, []byte(`{
		"schema_version":1,"status":"built","service":"web",
		"source_commit":"`+testCommit+`","source_image":"dev/web","source_digest":"`+testDigest+`"
	}`), 0o600))
	require.NoError(t, os.WriteFile(qa, []byte(`{
		"schema_version":1,"qa_status":"approved","service":"web",
		"source_commit":"`+testCommit+`","source_image":"dev/web",
		"source_digest":"`+testDigest+`","evidence_url":"https://example.test/qa"
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
		"source_commit":"`+testCommit+`","source_image":"dev/web","source_digest":"`+testDigest+`"
	}`), 0o600))
	require.NoError(t, os.WriteFile(qa, []byte(`{
		"schema_version":1,"qa_status":"approved","service":"web",
		"source_commit":"`+testCommit+`","source_image":"dev/web",
		"source_digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"evidence_url":"https://example.test/qa"
	}`), 0o600))

	_, err := ValidateStandard("web", testCommit, candidate, qa)
	require.ErrorContains(t, err, "exact candidate")
}

func TestValidateCandidateAcceptsCurrentSchema(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "candidate.json")
	require.NoError(t, os.WriteFile(path, []byte(`{
		"schema_version":2,"status":"built","service":"web",
		"source_commit":"`+testCommit+`","source_image":"candidate/web","source_digest":"`+testDigest+`"
	}`), 0o600))
	artifact, err := ValidateCandidate("web", testCommit, path)
	require.NoError(t, err)
	require.Equal(t, "candidate/web", artifact.Image)
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
