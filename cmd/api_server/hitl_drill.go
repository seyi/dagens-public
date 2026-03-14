package main

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/seyi/dagens/pkg/agent"
	"github.com/seyi/dagens/pkg/graph"
	"github.com/seyi/dagens/pkg/hitl"
	"github.com/seyi/dagens/pkg/scheduler"
)

type hitlFailoverDrillResponse struct {
	JobID       string `json:"job_id"`
	RequestID   string `json:"request_id"`
	CallbackURL string `json:"callback_url"`
	Status      string `json:"status"`
}

func newHITLFailoverDrillHandler(
	sched *scheduler.Scheduler,
	checkpointStore hitl.CheckpointStore,
	securityMgr *hitl.SecurityManager,
	baseURL string,
) http.Handler {
	// Consistency model:
	// 1. create durable checkpoint first
	// 2. submit scheduler job second
	// 3. if submit fails, checkpoint rollback is best-effort
	// 4. client retries are not idempotent unless an explicit idempotency layer is added
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if sched == nil || checkpointStore == nil || securityMgr == nil {
			log.Printf("HITL drill runtime unavailable: sched=%t checkpointStore=%t securityMgr=%t", sched != nil, checkpointStore != nil, securityMgr != nil)
			http.Error(w, "HITL drill runtime is not configured", http.StatusServiceUnavailable)
			return
		}

		jobID := "hitl-drill-" + uuid.NewString()
		requestID := "hitl-drill-req-" + uuid.NewString()

		state := graph.NewMemoryState()
		state.Set("scheduler_job_id", jobID)
		stateData, err := state.Marshal()
		if err != nil {
			log.Printf("HITL drill state marshal failed for job %s request %s: %v", jobID, requestID, err)
			http.Error(w, "failed to initialize drill", http.StatusInternalServerError)
			return
		}

		cp := &hitl.ExecutionCheckpoint{
			RequestID:    requestID,
			GraphID:      hitlDrillGraphID,
			GraphVersion: hitlDrillGraphVersion,
			NodeID:       "human",
			StateData:    stateData,
			CreatedAt:    time.Now().UTC(),
			ExpiresAt:    time.Now().UTC().Add(30 * time.Minute),
		}
		if err := checkpointStore.Create(cp); err != nil {
			log.Printf("HITL drill checkpoint create failed for job %s request %s: %v", jobID, requestID, err)
			http.Error(w, "failed to initialize drill", http.StatusInternalServerError)
			return
		}

		job := scheduler.NewJob(jobID, "ha-hitl-failover-drill")
		job.Metadata["request_id"] = requestID
		stage := &scheduler.Stage{
			ID:    "stage-1",
			JobID: jobID,
			Tasks: []*scheduler.Task{
				{
					ID:           "task-" + uuid.NewString(),
					StageID:      "stage-1",
					JobID:        jobID,
					AgentID:      "hitl-drill-human",
					AgentName:    "hitl-drill-human",
					PartitionKey: requestID,
					Input: &agent.AgentInput{
						Instruction: "hitl failover drill",
						Context: map[string]interface{}{
							"hitl_request_id":               requestID,
							"hitl_drill_resume_ready":       false,
							"hitl_callback_selected_option": "",
						},
					},
					Status: scheduler.JobPending,
				},
			},
		}
		job.AddStage(stage)

		if err := sched.SubmitJobWithContext(r.Context(), job); err != nil {
			if deleteErr := checkpointStore.Delete(requestID); deleteErr != nil {
				log.Printf("HITL drill checkpoint rollback failed for job %s request %s after submit error: %v (submit err: %v)", jobID, requestID, deleteErr, err)
			}
			log.Printf("HITL drill job submit failed for job %s request %s: %v", jobID, requestID, err)
			http.Error(w, "failed to initialize drill", http.StatusInternalServerError)
			return
		}

		callbackURL := securityMgr.GenerateCallbackURL(strings.TrimRight(baseURL, "/"), requestID)
		resp := hitlFailoverDrillResponse{
			JobID:       jobID,
			RequestID:   requestID,
			CallbackURL: callbackURL,
			Status:      string(job.Status),
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			log.Printf("HITL drill response encode failed for job %s request %s: %v", jobID, requestID, err)
		}
	})
}
