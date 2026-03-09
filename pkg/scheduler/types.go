package scheduler

import (
	"time"

	"github.com/seyi/dagens/pkg/agent"
)

// JobStatus represents the state of a job
type JobStatus string

const (
	JobPending       JobStatus = "PENDING"
	JobRunning       JobStatus = "RUNNING"
	JobCompleted     JobStatus = "COMPLETED"
	JobFailed        JobStatus = "FAILED"
	JobBlocked       JobStatus = "BLOCKED"
	JobAwaitingHuman JobStatus = "AWAITING_HUMAN"
	JobSuspended     JobStatus = "SUSPENDED"
)

// Job represents a distributed execution job derived from a Graph
type Job struct {
	ID             string
	Name           string
	Stages         []*Stage
	Edges          []Edge // Added to preserve logical structure for UI
	Status         JobStatus
	LifecycleState JobLifecycleState
	CreatedAt      time.Time
	UpdatedAt      time.Time
	Metadata       map[string]interface{}
}

// Edge represents a logical connection between agents
type Edge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Stage represents a group of tasks that can be executed in parallel
// In Spark terms, this is a set of tasks with no shuffle dependencies between them
type Stage struct {
	ID           string
	JobID        string
	Tasks        []*Task
	Dependencies []string // IDs of stages this stage depends on
	Status       JobStatus
}

// Task represents a single unit of work to be executed by an agent
type Task struct {
	ID             string
	StageID        string
	JobID          string
	AgentID        string
	AgentName      string
	Input          *agent.AgentInput
	Output         *agent.AgentOutput `json:"Output,omitempty"` // Added for result retrieval
	PartitionKey   string             // For locality-aware scheduling
	Status         JobStatus
	LifecycleState TaskLifecycleState
	Attempts       int
}

// NewJob creates a new empty job
func NewJob(id, name string) *Job {
	return &Job{
		ID:             id,
		Name:           name,
		Stages:         make([]*Stage, 0),
		Status:         JobPending,
		LifecycleState: JobStateSubmitted,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
		Metadata:       make(map[string]interface{}),
	}
}

// AddStage adds a stage to the job
func (j *Job) AddStage(stage *Stage) {
	j.Stages = append(j.Stages, stage)
}
