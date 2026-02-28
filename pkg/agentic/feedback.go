package agentic

import (
	"context"
	"sync"
	"time"
)

// FeedbackReceiver allows external systems (humans, automated evaluators, peer agents)
// to provide feedback to an agent. This enables learning loops without the framework
// needing to understand domain-specific concepts.
type FeedbackReceiver interface {
	// RecordFeedback records external feedback for a specific task.
	// score: normalized feedback score (0.0-1.0)
	// source: who provided the feedback ("human", "automated", "peer_agent")
	// comments: optional textual feedback
	RecordFeedback(ctx context.Context, taskID string, score float64, source string, comments string)

	// GetFeedback retrieves feedback for a task if available.
	GetFeedback(taskID string) (*Feedback, bool)

	// GetAverageScore returns the average feedback score across all recorded feedback.
	GetAverageScore() float64
}

// Feedback represents external feedback for a task.
type Feedback struct {
	TaskID    string
	Score     float64
	Source    string
	Comments  string
	Timestamp time.Time
}

// FeedbackSource constants
const (
	FeedbackSourceHuman     = "human"
	FeedbackSourceAutomated = "automated"
	FeedbackSourcePeerAgent = "peer_agent"
	FeedbackSourceHybrid    = "hybrid"
)

// SimpleFeedbackReceiver provides a basic in-memory implementation of FeedbackReceiver.
type SimpleFeedbackReceiver struct {
	mu       sync.RWMutex
	feedback map[string]*Feedback
	scores   []float64
}

// NewSimpleFeedbackReceiver creates a new in-memory feedback receiver.
func NewSimpleFeedbackReceiver() *SimpleFeedbackReceiver {
	return &SimpleFeedbackReceiver{
		feedback: make(map[string]*Feedback),
		scores:   make([]float64, 0),
	}
}

// RecordFeedback implements FeedbackReceiver.
func (r *SimpleFeedbackReceiver) RecordFeedback(ctx context.Context, taskID string, score float64, source string, comments string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	fb := &Feedback{
		TaskID:    taskID,
		Score:     score,
		Source:    source,
		Comments:  comments,
		Timestamp: time.Now(),
	}

	r.feedback[taskID] = fb
	r.scores = append(r.scores, score)
}

// GetFeedback implements FeedbackReceiver.
func (r *SimpleFeedbackReceiver) GetFeedback(taskID string) (*Feedback, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	fb, ok := r.feedback[taskID]
	return fb, ok
}

// GetAverageScore implements FeedbackReceiver.
func (r *SimpleFeedbackReceiver) GetAverageScore() float64 {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if len(r.scores) == 0 {
		return 0.0
	}

	var sum float64
	for _, s := range r.scores {
		sum += s
	}
	return sum / float64(len(r.scores))
}

// GetRecentFeedback returns feedback from the last n entries.
func (r *SimpleFeedbackReceiver) GetRecentFeedback(n int) []*Feedback {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]*Feedback, 0, n)
	for _, fb := range r.feedback {
		result = append(result, fb)
		if len(result) >= n {
			break
		}
	}
	return result
}

// Clear removes all feedback.
func (r *SimpleFeedbackReceiver) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.feedback = make(map[string]*Feedback)
	r.scores = make([]float64, 0)
}
