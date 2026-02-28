package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/seyi/dagens/pkg/agent"
	"github.com/seyi/dagens/pkg/graph"
	"github.com/seyi/dagens/pkg/scheduler"
)

// Server provides an HTTP API for job submission and monitoring
type Server struct {
	scheduler *scheduler.Scheduler
	compiler  *graph.DAGCompiler
}

// NewServer creates a new API server
func NewServer(sched *scheduler.Scheduler) *Server {
	return &Server{
		scheduler: sched,
		compiler:  graph.NewDAGCompiler(),
	}
}

// SubmitJobHandler handles job submission requests
func (s *Server) SubmitJobHandler(w http.ResponseWriter, r *http.Request) {
	var req JobSubmissionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
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
