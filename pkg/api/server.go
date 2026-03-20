package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/seyi/dagens/pkg/agent"
	"github.com/seyi/dagens/pkg/graph"
	"github.com/seyi/dagens/pkg/observability"
	"github.com/seyi/dagens/pkg/scheduler"
)

const schedulerRetryAfterSeconds = "5"
const schedulerLeaderRetryAfterSeconds = "1"
const defaultForwardTimeout = 10 * time.Second
const defaultMaxJobRequestBytes int64 = 2 << 20
const authorityLookupTimeout = 2 * time.Second
const authorityLookupAttempts = 3
const authorityLookupRetryBase = 100 * time.Millisecond

// Server provides an HTTP API for job submission and monitoring
type Server struct {
	scheduler                   *scheduler.Scheduler
	compiler                    *graph.DAGCompiler
	workerHeartbeatToken        string
	workerHeartbeatAuthRequired bool
	leaderForwardURLs           map[string]string
	controlPlaneID              string
	httpClient                  *http.Client
}

// NewServer creates a new API server
func NewServer(sched *scheduler.Scheduler) *Server {
	return &Server{
		scheduler:                   sched,
		compiler:                    graph.NewDAGCompiler(),
		workerHeartbeatToken:        os.Getenv("WORKER_HEARTBEAT_TOKEN"),
		workerHeartbeatAuthRequired: os.Getenv("DEV_MODE") != "true",
		leaderForwardURLs:           parseLeaderForwardURLs(os.Getenv("SCHEDULER_LEADER_FORWARD_URLS")),
		controlPlaneID:              strings.TrimSpace(os.Getenv("CONTROL_PLANE_ID")),
		httpClient:                  &http.Client{Timeout: forwardTimeoutFromEnv()},
	}
}

// SubmitJobHandler handles job submission requests
func (s *Server) SubmitJobHandler(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxJobRequestBytes())
	body, err := io.ReadAll(r.Body)
	if err != nil {
		status := http.StatusBadRequest
		message := "Failed to read request body"
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			status = http.StatusRequestEntityTooLarge
			message = "Request body too large"
		}
		http.Error(w, message, status)
		return
	}

	var req JobSubmissionRequest
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	authority, err := s.resolveDispatchAuthority(r.Context())
	if err != nil {
		w.Header().Set("Retry-After", schedulerLeaderRetryAfterSeconds)
		http.Error(w, "Submission temporarily unavailable: leadership status unavailable", http.StatusServiceUnavailable)
		return
	}
	if !authority.IsLeader {
		if s.tryForwardToLeader(w, r, body, authority) {
			return
		}
		w.Header().Set("Retry-After", schedulerLeaderRetryAfterSeconds)
		if authority.LeaderID != "" {
			w.Header().Set("X-Dagens-Leader-ID", authority.LeaderID)
		}
		http.Error(w, "Submission temporarily unavailable: scheduler follower mode", http.StatusServiceUnavailable)
		return
	}

	// 1. Build Graph from Request
	g := graph.NewGraph(req.Name)
	if req.Description != "" {
		g.SetMetadata("description", req.Description)
	}

	// Helper to track created nodes
	nodeMap := make(map[string]graph.Node)

	// Create nodes
	for _, nodeDef := range req.Nodes {
		var node graph.Node

		switch nodeDef.Type {
		case "function", "agent":
			// Create a function node as a placeholder
			fnNode := graph.NewFunctionNode(nodeDef.ID, func(ctx context.Context, state graph.State) error {
				return nil
			})
			if nodeDef.Name != "" {
				fnNode.SetName(nodeDef.Name)
			}

			// Apply metadata
			for k, v := range nodeDef.Metadata {
				fnNode.SetMetadata(k, v)
			}
			node = fnNode

		case "parallel":
			// Fallback for V1
			node = graph.NewFunctionNode(nodeDef.ID, nil)
		}

		if node != nil {
			g.AddNode(node)
			nodeMap[nodeDef.ID] = node
		}
	}

	// Set Entry Node
	if req.EntryNode != "" {
		if err := g.SetEntry(req.EntryNode); err != nil {
			http.Error(w, fmt.Sprintf("Invalid entry node: %v", err), http.StatusBadRequest)
			return
		}
	}

	// Set Finish Nodes
	for _, finishID := range req.FinishNodes {
		if err := g.AddFinish(finishID); err != nil {
			http.Error(w, fmt.Sprintf("Invalid finish node: %v", err), http.StatusBadRequest)
			return
		}
	}

	// Add Edges
	for _, edgeDef := range req.Edges {
		edge := graph.NewDirectEdge(edgeDef.From, edgeDef.To)
		if err := g.AddEdge(edge); err != nil {
			http.Error(w, fmt.Sprintf("Invalid edge from %s to %s: %v", edgeDef.From, edgeDef.To, err), http.StatusBadRequest)
			return
		}
	}

	// 2. Compile to Job
	input := &agent.AgentInput{
		Instruction: req.Input.Instruction,
		Context:     req.Input.Data,
	}

	// Use CompileWithOptions if SessionID is provided for sticky scheduling
	compileOpts := graph.CompileOptions{
		SessionID: req.SessionID,
	}

	job, err := s.compiler.CompileWithOptions(g, input, compileOpts)
	if err != nil {
		http.Error(w, fmt.Sprintf("Compilation failed: %v", err), http.StatusInternalServerError)
		return
	}

	// 3. Submit to Scheduler
	if err := s.scheduler.SubmitJob(job); err != nil {
		if errors.Is(err, scheduler.ErrJobQueueFull) {
			w.Header().Set("Retry-After", schedulerRetryAfterSeconds)
			http.Error(w, "Submission failed: job queue is full", http.StatusTooManyRequests)
			return
		}
		http.Error(w, fmt.Sprintf("Submission failed: %v", err), http.StatusInternalServerError)
		return
	}

	// 4. Return Response
	resp := JobResponse{
		JobID:       job.ID,
		Status:      string(job.Status),
		SubmittedAt: job.CreatedAt.Format(time.RFC3339),
		Message:     "Job submitted successfully",
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(resp)
}

func (s *Server) tryForwardToLeader(w http.ResponseWriter, r *http.Request, body []byte, authority scheduler.LeadershipAuthority) bool {
	if authority.LeaderID == "" {
		return false
	}
	targetBase := strings.TrimSpace(s.leaderForwardURLs[authority.LeaderID])
	if targetBase == "" || r.Header.Get("X-Dagens-Forwarded-By") != "" {
		return false
	}

	targetURL := strings.TrimRight(targetBase, "/") + r.URL.Path
	if r.URL.RawQuery != "" {
		targetURL += "?" + r.URL.RawQuery
	}

	req, err := http.NewRequestWithContext(r.Context(), r.Method, targetURL, bytes.NewReader(body))
	if err != nil {
		return false
	}
	req.Header = filteredForwardHeaders(r.Header)
	req.Header.Set("X-Dagens-Forwarded-By", s.controlPlaneID)
	req.Host = ""

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	copyHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
	return true
}

func parseLeaderForwardURLs(raw string) map[string]string {
	result := make(map[string]string)
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		parts := strings.SplitN(entry, "=", 2)
		if len(parts) != 2 {
			continue
		}
		leaderID := strings.TrimSpace(parts[0])
		baseURL := strings.TrimSpace(parts[1])
		if leaderID == "" || baseURL == "" {
			continue
		}
		result[leaderID] = baseURL
	}
	return result
}

func filteredForwardHeaders(src http.Header) http.Header {
	dst := make(http.Header)
	for key, values := range src {
		if shouldDropForwardHeader(key) {
			continue
		}
		for _, value := range values {
			dst.Add(key, value)
		}
	}
	return dst
}

func shouldDropForwardHeader(name string) bool {
	switch http.CanonicalHeaderKey(name) {
	case "Connection", "Proxy-Connection", "Keep-Alive", "Transfer-Encoding", "Te", "Trailer", "Upgrade",
		"Host", "Content-Length", "Cookie", "Set-Cookie", "X-Dagens-Worker-Token":
		return true
	default:
		return false
	}
}

func copyHeaders(dst, src http.Header) {
	for key, values := range src {
		dst.Del(key)
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func forwardTimeoutFromEnv() time.Duration {
	raw := strings.TrimSpace(os.Getenv("API_FORWARD_TIMEOUT"))
	if raw == "" {
		return defaultForwardTimeout
	}
	timeout, err := time.ParseDuration(raw)
	if err != nil || timeout <= 0 {
		log.Printf("Warning: invalid API_FORWARD_TIMEOUT %q: %v; using default %s", raw, err, defaultForwardTimeout)
		return defaultForwardTimeout
	}
	return timeout
}

func maxJobRequestBytes() int64 {
	raw := strings.TrimSpace(os.Getenv("API_MAX_JOB_REQUEST_BYTES"))
	if raw == "" {
		return defaultMaxJobRequestBytes
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 {
		log.Printf("Warning: invalid API_MAX_JOB_REQUEST_BYTES %q: %v; using default %d", raw, err, defaultMaxJobRequestBytes)
		return defaultMaxJobRequestBytes
	}
	return value
}

func (s *Server) resolveDispatchAuthority(ctx context.Context) (scheduler.LeadershipAuthority, error) {
	var authority scheduler.LeadershipAuthority
	var lastErr error
	for attempt := 0; attempt < authorityLookupAttempts; attempt++ {
		authorityCtx, cancel := context.WithTimeout(ctx, authorityLookupTimeout)
		authority, lastErr = s.scheduler.CurrentDispatchAuthority(authorityCtx)
		cancel()
		if lastErr == nil {
			return authority, nil
		}
		if attempt == authorityLookupAttempts-1 {
			break
		}
		select {
		case <-ctx.Done():
			return scheduler.LeadershipAuthority{}, ctx.Err()
		case <-time.After(authorityLookupRetryBase * time.Duration(1<<attempt)):
		}
	}
	return scheduler.LeadershipAuthority{}, lastErr
}

// ListJobsHandler returns all jobs
func (s *Server) ListJobsHandler(w http.ResponseWriter, r *http.Request) {
	jobs := s.scheduler.GetAllJobs()

	// Convert to simplified response if needed, or return full jobs
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(jobs)
}

// GetJobHandler returns job status
func (s *Server) GetJobHandler(w http.ResponseWriter, r *http.Request) {
	// Extract job ID from URL /v1/jobs/{id}
	id := r.URL.Path[len("/v1/jobs/"):]
	if id == "" {
		http.Error(w, "Job ID is required", http.StatusBadRequest)
		return
	}

	job, err := s.scheduler.GetJob(id)
	if err != nil {
		http.Error(w, fmt.Sprintf("Job not found: %v", err), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(job)
}

// SchedulerReadinessHandler returns the scheduler's current local readiness view.
func (s *Server) SchedulerReadinessHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.scheduler.Readiness())
}

// UpdateWorkerCapacityHandler accepts worker heartbeats with capacity snapshots.
func (s *Server) UpdateWorkerCapacityHandler(w http.ResponseWriter, r *http.Request) {
	metrics := observability.GetMetrics()
	metrics.RecordWorkerHeartbeatReceived()
	start := time.Now()
	defer func() {
		metrics.RecordWorkerHeartbeatProcessing(time.Since(start))
	}()

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.workerHeartbeatAuthRequired {
		if s.workerHeartbeatToken == "" {
			metrics.RecordWorkerHeartbeatAuthFailed()
			http.Error(w, "Worker heartbeat auth is not configured", http.StatusServiceUnavailable)
			return
		}
		if r.Header.Get("X-Dagens-Worker-Token") != s.workerHeartbeatToken {
			metrics.RecordWorkerHeartbeatAuthFailed()
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
	} else if s.workerHeartbeatToken != "" && r.Header.Get("X-Dagens-Worker-Token") != s.workerHeartbeatToken {
		metrics.RecordWorkerHeartbeatAuthFailed()
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	var req WorkerCapacityUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		metrics.RecordWorkerHeartbeatInvalidPayload()
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if req.NodeID == "" {
		metrics.RecordWorkerHeartbeatInvalidPayload()
		http.Error(w, "node_id is required", http.StatusBadRequest)
		return
	}
	if req.InFlight < 0 {
		metrics.RecordWorkerHeartbeatInvalidPayload()
		http.Error(w, "in_flight cannot be negative", http.StatusBadRequest)
		return
	}
	reportedAt := req.ReportTimestamp
	if reportedAt.IsZero() {
		reportedAt = time.Now()
	}
	if reportedAt.After(time.Now().Add(5 * time.Second)) {
		metrics.RecordWorkerHeartbeatInvalidPayload()
		http.Error(w, "report_timestamp is too far in the future", http.StatusBadRequest)
		return
	}

	s.scheduler.UpdateNodeCapacityAt(req.NodeID, req.InFlight, req.MaxConcurrency, reportedAt)
	metrics.RecordWorkerHeartbeatSucceeded()
	w.WriteHeader(http.StatusNoContent)
}

// Routes returns a ServeMux with configured routes
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	// Health check endpoint
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"healthy"}`))
	})

	mux.HandleFunc("/v1/jobs", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			s.SubmitJobHandler(w, r)
		} else if r.Method == http.MethodGet {
			s.ListJobsHandler(w, r)
		} else {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/v1/jobs/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			s.GetJobHandler(w, r)
		} else {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/v1/internal/scheduler_readiness", s.SchedulerReadinessHandler)
	mux.HandleFunc("/v1/internal/worker_capacity", s.UpdateWorkerCapacityHandler)

	// Wrap in CORS middleware
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Allow requests from the frontend
		w.Header().Set("Access-Control-Allow-Origin", "http://localhost:3000")
		w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS, PUT, DELETE")
		w.Header().Set("Access-Control-Allow-Headers", "Accept, Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization")
		w.Header().Set("Access-Control-Allow-Credentials", "true")

		// Handle preflight requests
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		mux.ServeHTTP(w, r)
	})
}
