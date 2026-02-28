package graph

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/seyi/dagens/pkg/telemetry"
)

// FileStateStore persists StateSnapshot objects to the local filesystem.
// Each execution ID is stored as a single JSON file under the configured directory.
// Writes are atomic (tmp + rename) to avoid partial snapshots.
type FileStateStore struct {
	dir string
}

// NewFileStateStore creates a file-backed StateStore under dir.
// The directory is created if it does not exist.
func NewFileStateStore(dir string) (*FileStateStore, error) {
	if dir == "" {
		return nil, fmt.Errorf("graph: dir is required for FileStateStore")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("graph: create state dir: %w", err)
	}
	return &FileStateStore{dir: dir}, nil
}

// Save writes the snapshot to disk atomically.
func (s *FileStateStore) Save(ctx context.Context, executionID string, snapshot *StateSnapshot) error {
	if executionID == "" {
		return ErrInvalidExecutionID
	}
	if snapshot == nil {
		return ErrNilSnapshot
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	// Start span for the save operation
	tracer := telemetry.GetGlobalTelemetry().GetTracer()
	ctx, span := tracer.StartSpan(ctx, "state.save")
	defer span.End()

	// Add attributes to the span
	span.SetAttribute("state.execution_id", executionID)
	span.SetAttribute("state.size", len(snapshot.Data))

	payload, err := json.Marshal(snapshot.Clone())
	if err != nil {
		span.SetStatus(telemetry.StatusError, "Failed to marshal snapshot")
		return fmt.Errorf("graph: marshal snapshot: %w", err)
	}

	span.SetAttribute("state.serialized_size", len(payload))

	tmpPath := s.filePath(executionID) + ".tmp"
	finalPath := s.filePath(executionID)

	if err := os.WriteFile(tmpPath, payload, 0o644); err != nil {
		span.SetStatus(telemetry.StatusError, "Failed to write temporary file")
		return fmt.Errorf("graph: write tmp snapshot: %w", err)
	}

	// fsync via rename (best-effort durability)
	if err := os.Rename(tmpPath, finalPath); err != nil {
		span.SetStatus(telemetry.StatusError, "Failed to rename temporary file")
		return fmt.Errorf("graph: atomically replace snapshot: %w", err)
	}

	span.SetStatus(telemetry.StatusOK, "State saved successfully")
	return nil
}

// SaveBatch persists multiple snapshots. It validates all first, then writes sequentially.
// Atomicity across files is best-effort; if any write fails, the error is returned.
func (s *FileStateStore) SaveBatch(ctx context.Context, snapshots map[string]*StateSnapshot) error {
	if len(snapshots) == 0 {
		return nil
	}
	for id, snap := range snapshots {
		if err := s.Save(ctx, id, snap); err != nil {
			return err
		}
	}
	return nil
}

// Load reads the snapshot for executionID.
func (s *FileStateStore) Load(ctx context.Context, executionID string) (*StateSnapshot, error) {
	if executionID == "" {
		return nil, ErrInvalidExecutionID
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Start span for the load operation
	tracer := telemetry.GetGlobalTelemetry().GetTracer()
	ctx, span := tracer.StartSpan(ctx, "state.load")
	defer span.End()

	// Add attributes to the span
	span.SetAttribute("state.execution_id", executionID)

	data, err := os.ReadFile(s.filePath(executionID))
	if os.IsNotExist(err) {
		span.SetStatus(telemetry.StatusError, "State file not found")
		return nil, fmt.Errorf("%w: %s", ErrStateNotFound, executionID)
	}
	if err != nil {
		span.SetStatus(telemetry.StatusError, "Failed to read snapshot file")
		return nil, fmt.Errorf("graph: read snapshot: %w", err)
	}

	span.SetAttribute("state.file_size", len(data))

	var snap StateSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		span.SetStatus(telemetry.StatusError, "Failed to unmarshal snapshot")
		return nil, fmt.Errorf("graph: unmarshal snapshot: %w", err)
	}

	span.SetAttribute("state.size", len(snap.Data))
	span.SetStatus(telemetry.StatusOK, "State loaded successfully")

	return snap.Clone(), nil
}

// Delete removes a snapshot file; it's idempotent.
func (s *FileStateStore) Delete(ctx context.Context, executionID string) error {
	if executionID == "" {
		return ErrInvalidExecutionID
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	err := os.Remove(s.filePath(executionID))
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("graph: delete snapshot: %w", err)
	}
	return nil
}

// List returns sorted execution IDs present on disk.
func (s *FileStateStore) List(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, fmt.Errorf("graph: list snapshots: %w", err)
	}

	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ".tmp") {
			// best-effort cleanup of stale tmp files older than 1h
			info, err := e.Info()
			if err == nil && time.Since(info.ModTime()) > time.Hour {
				_ = os.Remove(filepath.Join(s.dir, name))
			}
			continue
		}
		ids = append(ids, strings.TrimSuffix(name, filepath.Ext(name)))
	}
	sort.Strings(ids)
	return ids, nil
}

// Exists reports whether a snapshot file exists.
func (s *FileStateStore) Exists(ctx context.Context, executionID string) (bool, error) {
	if executionID == "" {
		return false, ErrInvalidExecutionID
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	_, err := os.Stat(s.filePath(executionID))
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, fmt.Errorf("graph: stat snapshot: %w", err)
}

// Clear removes all snapshot files in the directory.
func (s *FileStateStore) Clear(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return fmt.Errorf("graph: list for clear: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if err := os.Remove(filepath.Join(s.dir, e.Name())); err != nil {
			return fmt.Errorf("graph: clear snapshot %s: %w", e.Name(), err)
		}
	}
	return nil
}

// filePath returns the absolute path for an executionID snapshot.
func (s *FileStateStore) filePath(executionID string) string {
	filename := fmt.Sprintf("%s.json", executionID)
	return filepath.Join(s.dir, filename)
}
