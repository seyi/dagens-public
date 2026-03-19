package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/seyi/dagens/pkg/scheduler"
)

type snapshotConfig struct {
	DatabaseURL      string        `json:"database_url"`
	JobIDPrefix      string        `json:"job_id_prefix"`
	EvidenceRoot     string        `json:"evidence_root"`
	Timeout          time.Duration `json:"timeout"`
	ExpectedJobCount int           `json:"expected_job_count"`
}

type snapshotResult struct {
	TimestampUTC   string              `json:"timestamp_utc"`
	Config         snapshotConfig      `json:"config"`
	ReplayedJobs   int                 `json:"replayed_jobs"`
	StateCounts    map[string]int      `json:"state_counts"`
	SnapshotSHA256 string              `json:"snapshot_sha256"`
	Jobs           []replayedJobDigest `json:"jobs"`
}

type replayedJobDigest struct {
	JobID          string               `json:"job_id"`
	CurrentState   string               `json:"current_state"`
	LastSequenceID int64                `json:"last_sequence_id"`
	TaskCount      int                  `json:"task_count"`
	Tasks          []replayedTaskDigest `json:"tasks"`
}

type replayedTaskDigest struct {
	TaskID       string `json:"task_id"`
	CurrentState string `json:"current_state"`
	NodeID       string `json:"node_id"`
	LastAttempt  int    `json:"last_attempt"`
}

func main() {
	cfg := snapshotConfig{}
	flag.StringVar(&cfg.DatabaseURL, "database-url", os.Getenv("DATABASE_URL"), "Postgres DSN for scheduler transition store")
	flag.StringVar(&cfg.JobIDPrefix, "job-id-prefix", "", "required job_id prefix to snapshot")
	flag.StringVar(&cfg.EvidenceRoot, "evidence-root", defaultEvidenceRoot(), "directory to write snapshot evidence")
	flag.DurationVar(&cfg.Timeout, "timeout", 60*time.Second, "overall snapshot timeout")
	flag.IntVar(&cfg.ExpectedJobCount, "expected-job-count", 0, "optional expected replayed job count")
	flag.Parse()

	if cfg.DatabaseURL == "" {
		exitf("database-url is required")
	}
	if cfg.JobIDPrefix == "" {
		exitf("job-id-prefix is required")
	}
	if err := os.MkdirAll(cfg.EvidenceRoot, 0o755); err != nil {
		exitf("create evidence root: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
	defer cancel()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		exitf("create pgx pool: %v", err)
	}
	defer pool.Close()

	store, err := scheduler.NewPostgresTransitionStore(ctx, pool)
	if err != nil {
		exitf("create postgres transition store: %v", err)
	}

	replayed, err := scheduler.ReplayStateFromStore(ctx, store)
	if err != nil {
		exitf("replay state from store: %v", err)
	}

	result := snapshotResult{
		TimestampUTC: time.Now().UTC().Format(time.RFC3339),
		Config:       cfg,
		StateCounts:  map[string]int{},
		Jobs:         make([]replayedJobDigest, 0),
	}

	for _, job := range replayed {
		if !strings.HasPrefix(job.Job.JobID, cfg.JobIDPrefix) {
			continue
		}
		digest := replayedJobDigest{
			JobID:          job.Job.JobID,
			CurrentState:   string(job.Job.CurrentState),
			LastSequenceID: int64(job.Job.LastSequenceID),
			Tasks:          make([]replayedTaskDigest, 0, len(job.Tasks)),
		}
		taskIDs := make([]string, 0, len(job.Tasks))
		for taskID := range job.Tasks {
			taskIDs = append(taskIDs, taskID)
		}
		sort.Strings(taskIDs)
		for _, taskID := range taskIDs {
			task := job.Tasks[taskID]
			digest.Tasks = append(digest.Tasks, replayedTaskDigest{
				TaskID:       taskID,
				CurrentState: string(task.CurrentState),
				NodeID:       task.NodeID,
				LastAttempt:  task.LastAttempt,
			})
		}
		digest.TaskCount = len(digest.Tasks)
		result.Jobs = append(result.Jobs, digest)
		result.StateCounts[digest.CurrentState]++
	}

	sort.Slice(result.Jobs, func(i, j int) bool {
		return result.Jobs[i].JobID < result.Jobs[j].JobID
	})
	result.ReplayedJobs = len(result.Jobs)
	if cfg.ExpectedJobCount > 0 && result.ReplayedJobs != cfg.ExpectedJobCount {
		exitf("expected %d replayed jobs for prefix %s, got %d", cfg.ExpectedJobCount, cfg.JobIDPrefix, result.ReplayedJobs)
	}

	hashInput, err := json.Marshal(result.Jobs)
	if err != nil {
		exitf("marshal replay digest for hashing: %v", err)
	}
	sum := sha256.Sum256(hashInput)
	result.SnapshotSHA256 = hex.EncodeToString(sum[:])

	if err := writeResults(cfg.EvidenceRoot, result); err != nil {
		exitf("write results: %v", err)
	}

	fmt.Printf("PASS: transition replay snapshot completed\n")
	fmt.Printf("evidence_root=%s\n", cfg.EvidenceRoot)
	fmt.Printf("job_id_prefix=%s\n", cfg.JobIDPrefix)
	fmt.Printf("replayed_jobs=%d snapshot_sha256=%s\n", result.ReplayedJobs, result.SnapshotSHA256)
}

func defaultEvidenceRoot() string {
	return filepath.Join(os.TempDir(), fmt.Sprintf("dagens-transition-replay-snapshot-%s", time.Now().UTC().Format("20060102T150405Z")))
}

func writeResults(evidenceRoot string, result snapshotResult) error {
	encoded, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(evidenceRoot, "result.json"), append(encoded, '\n'), 0o644); err != nil {
		return err
	}
	summary := fmt.Sprintf(
		"status=PASS\n"+
			"evidence_root=%s\n"+
			"job_id_prefix=%s\n"+
			"replayed_jobs=%d\n"+
			"snapshot_sha256=%s\n",
		evidenceRoot,
		result.Config.JobIDPrefix,
		result.ReplayedJobs,
		result.SnapshotSHA256,
	)
	return os.WriteFile(filepath.Join(evidenceRoot, "summary.txt"), []byte(summary), 0o644)
}

func exitf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
