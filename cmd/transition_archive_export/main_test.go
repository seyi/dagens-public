package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestShouldVerifyBundles(t *testing.T) {
	tests := []struct {
		name string
		cfg  exportConfig
		want bool
	}{
		{
			name: "enabled by default path",
			cfg:  exportConfig{VerifyBundles: true, DryRun: false},
			want: true,
		},
		{
			name: "disabled explicitly",
			cfg:  exportConfig{VerifyBundles: false, DryRun: false},
			want: false,
		},
		{
			name: "dry run suppresses verification",
			cfg:  exportConfig{VerifyBundles: true, DryRun: true},
			want: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldVerifyBundles(tc.cfg); got != tc.want {
				t.Fatalf("shouldVerifyBundles(%+v) = %v, want %v", tc.cfg, got, tc.want)
			}
		})
	}
}

func TestVerifyManifestSuccess(t *testing.T) {
	evidenceRoot := t.TempDir()
	bundle := sampleArchiveBundle()
	manifest := writeTestBundle(t, evidenceRoot, bundle)

	verified, err := verifyManifest(evidenceRoot, manifest)
	if err != nil {
		t.Fatalf("verifyManifest returned error: %v", err)
	}
	if !verified.Enabled || !verified.Verified || !verified.ManifestChecksPassed {
		t.Fatalf("unexpected verification result: %+v", verified)
	}
	if verified.VerifiedBundles != 1 {
		t.Fatalf("verified bundles = %d, want 1", verified.VerifiedBundles)
	}
	if verified.VerifiedBytes <= 0 {
		t.Fatalf("verified bytes = %d, want > 0", verified.VerifiedBytes)
	}
}

func TestVerifyManifestChecksumMismatch(t *testing.T) {
	evidenceRoot := t.TempDir()
	bundle := sampleArchiveBundle()
	manifest := writeTestBundle(t, evidenceRoot, bundle)
	manifest.Bundles[0].SHA256 = strings.Repeat("0", 64)

	_, err := verifyManifest(evidenceRoot, manifest)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("expected checksum mismatch, got %v", err)
	}
}

func TestVerifyManifestRowCountMismatch(t *testing.T) {
	evidenceRoot := t.TempDir()
	bundle := sampleArchiveBundle()
	manifest := writeTestBundle(t, evidenceRoot, bundle)
	manifest.Bundles[0].TransitionRows++

	_, err := verifyManifest(evidenceRoot, manifest)
	if err == nil || !strings.Contains(err.Error(), "transition_rows mismatch") {
		t.Fatalf("expected transition_rows mismatch, got %v", err)
	}
}

func sampleArchiveBundle() archiveBundle {
	return archiveBundle{
		SchemaVersion:    archiveSchemaVersion,
		ArchiveTimestamp: "2026-03-18T00:00:00Z",
		HotRetentionDays: 14,
		Job: durableJobRow{
			JobID:          "job-1",
			Name:           "job",
			CurrentState:   "SUCCEEDED",
			LastSequenceID: 2,
			CreatedAtUTC:   "2026-03-01T00:00:00Z",
			UpdatedAtUTC:   "2026-03-02T00:00:00Z",
		},
		Tasks: []durableTaskRow{
			{
				TaskID:       "task-1",
				JobID:        "job-1",
				StageID:      "stage-1",
				CurrentState: "SUCCEEDED",
				LastAttempt:  1,
				UpdatedAtUTC: "2026-03-02T00:00:00Z",
			},
		},
		Transitions: []transitionRow{
			{JobID: "job-1", SequenceID: 1, EntityType: "job", Transition: "JOB_SUBMITTED", NewState: "SUBMITTED", OccurredAtUTC: "2026-03-01T00:00:00Z"},
			{JobID: "job-1", SequenceID: 2, EntityType: "job", Transition: "JOB_SUCCEEDED", PreviousState: "RUNNING", NewState: "SUCCEEDED", OccurredAtUTC: "2026-03-02T00:00:00Z"},
		},
		Sequences: []jobSequenceRow{
			{JobID: "job-1", LastSequenceID: 2, UpdatedAtUTC: "2026-03-02T00:00:00Z"},
		},
	}
}

func writeTestBundle(t *testing.T, evidenceRoot string, bundle archiveBundle) exportManifest {
	t.Helper()
	bundleDir := filepath.Join(evidenceRoot, "bundles")
	if err := os.MkdirAll(bundleDir, 0o755); err != nil {
		t.Fatalf("mkdir bundleDir: %v", err)
	}
	relativePath := filepath.Join("bundles", "job-1.json")
	targetPath := filepath.Join(evidenceRoot, relativePath)

	encoded, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		t.Fatalf("marshal bundle: %v", err)
	}
	payload := append(encoded, '\n')
	if err := os.WriteFile(targetPath, payload, 0o644); err != nil {
		t.Fatalf("write bundle: %v", err)
	}
	sum := sha256.Sum256(payload)

	return exportManifest{
		SchemaVersion:    archiveSchemaVersion,
		ArchiveTimestamp: bundle.ArchiveTimestamp,
		HotRetentionDays: bundle.HotRetentionDays,
		BatchLimit:       1,
		SelectedJobs:     1,
		ExportedJobs:     1,
		BundleDir:        bundleDir,
		Bundles: []exportedBundle{
			{
				JobID:          bundle.Job.JobID,
				CurrentState:   bundle.Job.CurrentState,
				UpdatedAtUTC:   bundle.Job.UpdatedAtUTC,
				RelativePath:   relativePath,
				SHA256:         hex.EncodeToString(sum[:]),
				TransitionRows: len(bundle.Transitions),
				TaskRows:       len(bundle.Tasks),
				SequenceRows:   len(bundle.Sequences),
				LastSequenceID: bundle.Job.LastSequenceID,
			},
		},
	}
}
