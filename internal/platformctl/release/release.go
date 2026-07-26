package release

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"time"
)

var (
	commitPattern    = regexp.MustCompile(`^[0-9a-f]{40}$`)
	digestPattern    = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	componentPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	validLanes       = map[string]struct{}{"standard": {}, "emergency": {}, "rollback": {}}
)

type Candidate struct {
	SchemaVersion int    `json:"schema_version"`
	Status        string `json:"status"`
	Service       string `json:"service"`
	SourceCommit  string `json:"source_commit"`
	SourceImage   string `json:"source_image"`
	SourceDigest  string `json:"source_digest"`
}

type QAEvidence struct {
	SchemaVersion  int    `json:"schema_version"`
	QAStatus       string `json:"qa_status"`
	Service        string `json:"service"`
	SourceCommit   string `json:"source_commit"`
	SourceImage    string `json:"source_image"`
	SourceDigest   string `json:"source_digest"`
	EvidenceURL    string `json:"evidence_url"`
	DevImage       string `json:"dev_image"`
	DevDigest      string `json:"dev_digest"`
	DevEvidenceURL string `json:"dev_evidence_url"`
}

type DevRelease struct {
	SchemaVersion int    `json:"schema_version"`
	Status        string `json:"status"`
	Service       string `json:"service"`
	SourceCommit  string `json:"source_commit"`
	SourceImage   string `json:"source_image"`
	SourceDigest  string `json:"source_digest"`
	DevImage      string `json:"dev_image"`
	DevDigest     string `json:"dev_digest"`
	WorkflowRun   string `json:"workflow_run"`
}

type HistoricalRelease struct {
	SchemaVersion int    `json:"schema_version"`
	Service       string `json:"service"`
	SourceCommit  string `json:"source_commit"`
	ProdImage     string `json:"prod_image"`
	ProdDigest    string `json:"prod_digest"`
}

type Artifact struct {
	Service      string `json:"service"`
	SourceCommit string `json:"source_commit"`
	Image        string `json:"image"`
	Digest       string `json:"digest"`
}

type Manifest struct {
	SchemaVersion int    `json:"schema_version"`
	Lane          string `json:"lane"`
	Service       string `json:"service"`
	SourceCommit  string `json:"source_commit"`
	ProdImage     string `json:"prod_image"`
	ProdDigest    string `json:"prod_digest"`
	Baseline      string `json:"baseline,omitempty"`
	WorkflowRun   string `json:"workflow_run"`
	RecordedAt    string `json:"recorded_at"`
}

func ValidateIdentity(lane, service, sourceCommit string) error {
	if _, ok := validLanes[lane]; !ok {
		return fmt.Errorf("unsupported release lane %q", lane)
	}
	if !componentPattern.MatchString(service) {
		return fmt.Errorf("invalid component name %q", service)
	}
	if !commitPattern.MatchString(sourceCommit) {
		return errors.New("source commit must be a full lowercase 40-character Git SHA")
	}
	return nil
}

func ValidateStandard(service, sourceCommit, candidatePath, qaPath string) (Artifact, error) {
	if err := ValidateIdentity("standard", service, sourceCommit); err != nil {
		return Artifact{}, err
	}
	candidateArtifact, err := ValidateCandidate(service, sourceCommit, candidatePath)
	if err != nil {
		return Artifact{}, err
	}
	var qa QAEvidence
	if err := readJSON(qaPath, &qa); err != nil {
		return Artifact{}, err
	}
	if qa.SchemaVersion != 3 || qa.QAStatus != "approved" ||
		qa.Service != service || qa.SourceCommit != sourceCommit ||
		qa.SourceImage != candidateArtifact.Image || qa.SourceDigest != candidateArtifact.Digest ||
		qa.DevImage == "" || qa.DevDigest != candidateArtifact.Digest ||
		qa.EvidenceURL == "" || qa.DevEvidenceURL == "" {
		return Artifact{}, errors.New("QA evidence does not approve the exact candidate digest verified in DEV")
	}
	return candidateArtifact, nil
}

func ValidateDevRelease(service, sourceCommit, candidatePath, devReleasePath string) (DevRelease, error) {
	candidate, err := ValidateCandidate(service, sourceCommit, candidatePath)
	if err != nil {
		return DevRelease{}, err
	}
	var release DevRelease
	if err := readJSON(devReleasePath, &release); err != nil {
		return DevRelease{}, err
	}
	if release.SchemaVersion != 1 || release.Status != "copied" || release.Service != service ||
		release.SourceCommit != sourceCommit || release.SourceImage != candidate.Image ||
		release.SourceDigest != candidate.Digest || release.DevImage == "" ||
		release.DevDigest != candidate.Digest || release.WorkflowRun == "" {
		return DevRelease{}, errors.New("DEV release does not contain an exact copy of the requested candidate digest")
	}
	return release, nil
}

func ValidateCandidate(service, sourceCommit, candidatePath string) (Artifact, error) {
	if err := ValidateIdentity("standard", service, sourceCommit); err != nil {
		return Artifact{}, err
	}
	var candidate Candidate
	if err := readJSON(candidatePath, &candidate); err != nil {
		return Artifact{}, err
	}
	if (candidate.SchemaVersion != 1 && candidate.SchemaVersion != 2) || candidate.Status != "built" ||
		candidate.Service != service || candidate.SourceCommit != sourceCommit ||
		candidate.SourceImage == "" || !digestPattern.MatchString(candidate.SourceDigest) {
		return Artifact{}, errors.New("candidate metadata does not match the requested immutable artifact")
	}
	return Artifact{
		Service: service, SourceCommit: sourceCommit,
		Image: candidate.SourceImage, Digest: candidate.SourceDigest,
	}, nil
}

func ValidateRollback(service, sourceCommit, historyPath string) (Artifact, error) {
	if err := ValidateIdentity("rollback", service, sourceCommit); err != nil {
		return Artifact{}, err
	}
	var history HistoricalRelease
	if err := readJSON(historyPath, &history); err != nil {
		return Artifact{}, err
	}
	if history.SchemaVersion < 1 || history.Service != service ||
		history.SourceCommit != sourceCommit || history.ProdImage == "" ||
		!digestPattern.MatchString(history.ProdDigest) {
		return Artifact{}, errors.New("release history does not contain the requested immutable PROD artifact")
	}
	return Artifact{
		Service: service, SourceCommit: sourceCommit,
		Image: history.ProdImage, Digest: history.ProdDigest,
	}, nil
}

func NewManifest(lane, service, sourceCommit, image, digest, baseline, workflowRun, recordedAt string) (Manifest, error) {
	if err := ValidateIdentity(lane, service, sourceCommit); err != nil {
		return Manifest{}, err
	}
	if image == "" || !digestPattern.MatchString(digest) {
		return Manifest{}, errors.New("PROD image and sha256 digest are required")
	}
	if baseline != "" && !commitPattern.MatchString(baseline) {
		return Manifest{}, errors.New("baseline must be a full lowercase 40-character Git SHA")
	}
	if workflowRun == "" {
		return Manifest{}, errors.New("workflow run URL is required")
	}
	if recordedAt == "" {
		recordedAt = time.Now().UTC().Format(time.RFC3339)
	} else if _, err := time.Parse(time.RFC3339, recordedAt); err != nil {
		return Manifest{}, fmt.Errorf("recorded-at must be RFC3339: %w", err)
	}
	return Manifest{
		SchemaVersion: 2, Lane: lane, Service: service, SourceCommit: sourceCommit,
		ProdImage: image, ProdDigest: digest, Baseline: baseline,
		WorkflowRun: workflowRun, RecordedAt: recordedAt,
	}, nil
}

func readJSON(path string, target any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}
