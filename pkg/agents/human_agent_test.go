package agents

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/seyi/dagens/pkg/agent"
	"github.com/seyi/dagens/pkg/events"
)

// Test Human-in-the-Loop Pattern Implementation
// Critical testing with distributed computing scenarios

// Test 1: Basic HumanAgent Creation and Configuration
func TestHumanAgentCreation(t *testing.T) {
	manager := NewHumanResponseManager()

	config := HumanAgentConfig{
		Name:            "ApprovalAgent",
		Description:     "Requests human approval",
		CallbackURL:     "https://approval-system.com/callback",
		Timeout:         30 * time.Second,
		ResponseManager: manager,
		Partition:       "approval-partition",
	}

	humanAgent := NewHumanAgent(config)

	if humanAgent.Name() != "ApprovalAgent" {
		t.Errorf("Expected name 'ApprovalAgent', got '%s'", humanAgent.Name())
	}

	if humanAgent.Partition() != "approval-partition" {
		t.Errorf("Expected partition 'approval-partition', got '%s'", humanAgent.Partition())
	}

	if humanAgent.GetResponseManager() != manager {
		t.Error("Response manager not set correctly")
	}

	t.Logf("✓ HumanAgent created successfully")
	t.Logf("  Name: %s", humanAgent.Name())
	t.Logf("  Partition: %s", humanAgent.Partition())
}

// Test 2: Synchronous Human Response
func TestHumanAgentSynchronousResponse(t *testing.T) {
	manager := NewHumanResponseManager()

	humanAgent := NewHumanAgent(HumanAgentConfig{
		Name:            "ReviewAgent",
		Description:     "Requests document review",
		Timeout:         5 * time.Second,
		ResponseManager: manager,
	})

	ctx := context.Background()
	input := &agent.AgentInput{
		Instruction: "Please review this document",
		Context: map[string]interface{}{
			"document_id": "doc-123",
		},
	}

	// Simulate human response in background
	go func() {
		time.Sleep(100 * time.Millisecond) // Simulate human thinking time
		pendingRequests := manager.ListPendingRequests()
		if len(pendingRequests) == 0 {
			t.Error("No pending requests found")
			return
		}

		requestID := pendingRequests[0]
		response := &HumanResponse{
			RequestID: requestID,
			Response:  "Approved",
			Metadata: map[string]interface{}{
				"reviewer": "John Doe",
				"comments": "Looks good",
			},
			Timestamp: time.Now(),
		}

		manager.DeliverResponse(response)
	}()

	// Execute agent (will wait for human response)
	output, err := humanAgent.Execute(ctx, input)
	if err != nil {
		t.Fatalf("Execution failed: %v", err)
	}

	if output.Result != "Approved" {
		t.Errorf("Expected 'Approved', got '%v'", output.Result)
	}

	humanMetadata := output.Metadata["human_metadata"].(map[string]interface{})
	if humanMetadata["reviewer"] != "John Doe" {
		t.Errorf("Expected reviewer 'John Doe', got '%v'", humanMetadata["reviewer"])
	}

	if output.Metadata["timed_out"].(bool) {
		t.Error("Should not have timed out")
	}

	t.Logf("✓ Synchronous human response successful")
	t.Logf("  Response: %v", output.Result)
	t.Logf("  Reviewer: %v", humanMetadata["reviewer"])
}

// Test 3: Timeout Handling with Default Response
func TestHumanAgentTimeout(t *testing.T) {
	manager := NewHumanResponseManager()

	humanAgent := NewHumanAgent(HumanAgentConfig{
		Name:            "TimeoutAgent",
		Description:     "Test timeout behavior",
		Timeout:         200 * time.Millisecond,
		DefaultResponse: "Auto-approved (timeout)",
		ResponseManager: manager,
	})

	ctx := context.Background()
	input := &agent.AgentInput{
		Instruction: "Approve this request",
	}

	startTime := time.Now()

	// Execute without providing response (will timeout)
	output, err := humanAgent.Execute(ctx, input)
	duration := time.Since(startTime)

	if err != nil {
		t.Fatalf("Should not error with default response: %v", err)
	}

	if duration < 200*time.Millisecond {
		t.Errorf("Should have waited for timeout, but completed in %v", duration)
	}

	if output.Result != "Auto-approved (timeout)" {
		t.Errorf("Expected default response, got '%v'", output.Result)
	}

	if !output.Metadata["timed_out"].(bool) {
		t.Error("timed_out metadata should be true")
	}

	t.Logf("✓ Timeout handling successful")
	t.Logf("  Duration: %v", duration)
	t.Logf("  Default response used: %v", output.Result)
}

// Test 4: Timeout Without Default Response (Error)
func TestHumanAgentTimeoutError(t *testing.T) {
	manager := NewHumanResponseManager()

	humanAgent := NewHumanAgent(HumanAgentConfig{
		Name:            "StrictAgent",
		Timeout:         100 * time.Millisecond,
		ResponseManager: manager,
	})

	ctx := context.Background()
	input := &agent.AgentInput{Instruction: "Approve"}

	// Execute without response or default
	_, err := humanAgent.Execute(ctx, input)

	if err == nil {
		t.Error("Expected timeout error")
	}

	if !strings.Contains(err.Error(), "timeout") {
		t.Errorf("Expected timeout error, got: %v", err)
	}

	t.Logf("✓ Timeout error handling correct")
	t.Logf("  Error: %v", err)
}

// Test 5: Event Bus Integration
func TestHumanAgentEventTracking(t *testing.T) {
	manager := NewHumanResponseManager()
	eventBus := events.NewMemoryEventBus()
	defer eventBus.Close()

	var requestSentEvent, responseReceivedEvent bool
	var eventLock sync.Mutex

	// Track request sent events
	eventBus.Subscribe(events.EventType("human.request_sent"), func(ctx context.Context, event events.Event) error {
		eventLock.Lock()
		defer eventLock.Unlock()
		requestSentEvent = true
		t.Logf("Request sent event: %v", event.Data())
		return nil
	})

	// Track response received events
	eventBus.Subscribe(events.EventType("human.response_received"), func(ctx context.Context, event events.Event) error {
		eventLock.Lock()
		defer eventLock.Unlock()
		responseReceivedEvent = true
		t.Logf("Response received event: %v", event.Data())
		return nil
	})

	humanAgent := NewHumanAgent(HumanAgentConfig{
		Name:            "EventAgent",
		Timeout:         2 * time.Second,
		ResponseManager: manager,
		EventBus:        eventBus,
	})

	ctx := context.Background()
	input := &agent.AgentInput{Instruction: "Test event tracking"}

	// Provide response immediately
	go func() {
		time.Sleep(50 * time.Millisecond)
		requests := manager.ListPendingRequests()
		if len(requests) > 0 {
			manager.DeliverResponse(&HumanResponse{
				RequestID: requests[0],
				Response:  "Tracked",
				Timestamp: time.Now(),
			})
		}
	}()

	_, err := humanAgent.Execute(ctx, input)
	if err != nil {
		t.Fatalf("Execution failed: %v", err)
	}

	// Small delay for event processing
	time.Sleep(50 * time.Millisecond)

	eventLock.Lock()
	defer eventLock.Unlock()

	if !requestSentEvent {
		t.Error("Request sent event not published")
	}

	if !responseReceivedEvent {
		t.Error("Response received event not published")
	}

	t.Logf("✓ Event tracking successful")
}

// Test 6: Distributed Scenario - Cross-Partition Human Requests
func TestDistributedHumanAgent(t *testing.T) {
	manager := NewHumanResponseManager()

	// Create agents on different partitions
	agent1 := NewHumanAgent(HumanAgentConfig{
		Name:            "Agent-Partition-0",
		Partition:       "partition-0",
		Timeout:         3 * time.Second,
		ResponseManager: manager, // Shared manager (simulates distributed coordination)
	})

	agent2 := NewHumanAgent(HumanAgentConfig{
		Name:            "Agent-Partition-1",
		Partition:       "partition-1",
		Timeout:         3 * time.Second,
		ResponseManager: manager,
	})

	agent3 := NewHumanAgent(HumanAgentConfig{
		Name:            "Agent-Partition-2",
		Partition:       "partition-2",
		Timeout:         3 * time.Second,
		ResponseManager: manager,
	})

	ctx := context.Background()

	// Execute all agents in parallel (simulating distributed execution)
	var wg sync.WaitGroup
	results := make(map[string]interface{})
	var resultLock sync.Mutex

	// Background goroutine to answer all requests
	go func() {
		time.Sleep(100 * time.Millisecond)
		for i := 0; i < 3; i++ {
			time.Sleep(50 * time.Millisecond)
			requests := manager.ListPendingRequests()
			if len(requests) > 0 {
				requestID := requests[0]
				request, exists := manager.GetPendingRequest(requestID)
				if exists {
					response := fmt.Sprintf("Approved from %s", request.Partition)
					manager.DeliverResponse(&HumanResponse{
						RequestID: requestID,
						Response:  response,
						Timestamp: time.Now(),
					})
				}
			}
		}
	}()

	// Execute all agents concurrently
	executeAgent := func(ag *HumanAgent, partition string) {
		defer wg.Done()
		output, err := ag.Execute(ctx, &agent.AgentInput{
			Instruction: fmt.Sprintf("Request from %s", partition),
		})
		if err != nil {
			t.Errorf("Agent on %s failed: %v", partition, err)
			return
		}

		resultLock.Lock()
		results[partition] = output.Result
		resultLock.Unlock()
	}

	wg.Add(3)
	go executeAgent(agent1, "partition-0")
	go executeAgent(agent2, "partition-1")
	go executeAgent(agent3, "partition-2")

	wg.Wait()

	// Verify all partitions received responses
	if len(results) != 3 {
		t.Errorf("Expected 3 results, got %d", len(results))
	}

	for partition, result := range results {
		if !strings.Contains(fmt.Sprintf("%v", result), partition) {
			t.Errorf("Result for %s doesn't match: %v", partition, result)
		}
	}

	t.Logf("✓ Distributed cross-partition execution successful")
	t.Logf("  Partitions executed: %d", len(results))
	for partition, result := range results {
		t.Logf("  %s: %v", partition, result)
	}
}

// Test 7: Multiple Concurrent Human Requests (Thread Safety)
func TestConcurrentHumanRequests(t *testing.T) {
	manager := NewHumanResponseManager()

	humanAgent := NewHumanAgent(HumanAgentConfig{
		Name:            "ConcurrentAgent",
		Timeout:         5 * time.Second,
		ResponseManager: manager,
	})

	ctx := context.Background()
	numRequests := 20
	var wg sync.WaitGroup

	// Background responder
	go func() {
		for i := 0; i < numRequests; i++ {
			time.Sleep(10 * time.Millisecond)
			requests := manager.ListPendingRequests()
			if len(requests) > 0 {
				for _, requestID := range requests {
					manager.DeliverResponse(&HumanResponse{
						RequestID: requestID,
						Response:  fmt.Sprintf("Response-%s", requestID),
						Timestamp: time.Now(),
					})
				}
			}
		}
	}()

	// Launch concurrent requests
	successCount := 0
	var countLock sync.Mutex

	for i := 0; i < numRequests; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			output, err := humanAgent.Execute(ctx, &agent.AgentInput{
				Instruction: fmt.Sprintf("Request %d", idx),
			})

			if err == nil {
				countLock.Lock()
				successCount++
				countLock.Unlock()
			}

			// Verify response
			if output != nil && !strings.Contains(fmt.Sprintf("%v", output.Result), "Response-") {
				t.Errorf("Unexpected response: %v", output.Result)
			}
		}(i)
	}

	wg.Wait()

	if successCount != numRequests {
		t.Errorf("Expected %d successful requests, got %d", numRequests, successCount)
	}

	t.Logf("✓ Concurrent request handling successful")
	t.Logf("  Total requests: %d", numRequests)
	t.Logf("  Successful: %d", successCount)
}

// Test 8: External Notifier Integration
func TestExternalNotifier(t *testing.T) {
	manager := NewHumanResponseManager()

	var notifiedRequests []*HumanRequest
	var notifyLock sync.Mutex

	// Set up external notifier
	manager.SetExternalNotifier(func(request *HumanRequest) error {
		notifyLock.Lock()
		defer notifyLock.Unlock()
		notifiedRequests = append(notifiedRequests, request)
		t.Logf("External system notified: %s - %s", request.RequestID, request.Instruction)
		return nil
	})

	humanAgent := NewHumanAgent(HumanAgentConfig{
		Name:            "NotifierAgent",
		Timeout:         2 * time.Second,
		ResponseManager: manager,
		CallbackURL:     "https://approval-ui.com/callback",
	})

	ctx := context.Background()

	// Execute request
	go func() {
		time.Sleep(100 * time.Millisecond)
		requests := manager.ListPendingRequests()
		if len(requests) > 0 {
			manager.DeliverResponse(&HumanResponse{
				RequestID: requests[0],
				Response:  "Notified and approved",
				Timestamp: time.Now(),
			})
		}
	}()

	_, err := humanAgent.Execute(ctx, &agent.AgentInput{
		Instruction: "Test external notification",
		Context: map[string]interface{}{
			"priority": "high",
		},
	})

	if err != nil {
		t.Fatalf("Execution failed: %v", err)
	}

	notifyLock.Lock()
	defer notifyLock.Unlock()

	if len(notifiedRequests) != 1 {
		t.Errorf("Expected 1 notification, got %d", len(notifiedRequests))
	}

	if len(notifiedRequests) > 0 {
		req := notifiedRequests[0]
		if req.Instruction != "Test external notification" {
			t.Errorf("Wrong instruction notified: %s", req.Instruction)
		}
		if req.CallbackURL != "https://approval-ui.com/callback" {
			t.Errorf("Wrong callback URL: %s", req.CallbackURL)
		}
	}

	t.Logf("✓ External notifier integration successful")
}

// Test 9: Request Formatting
func TestRequestFormatting(t *testing.T) {
	request := &HumanRequest{
		RequestID:   "test-123",
		AgentName:   "FormatAgent",
		Partition:   "format-partition",
		Instruction: "Choose an option",
		Options:     []string{"Option A", "Option B", "Option C"},
		Context: map[string]interface{}{
			"user_id": "user-456",
		},
		Timeout:   5 * time.Minute,
		Timestamp: time.Now(),
	}

	testFormats := []struct {
		format   RequestFormat
		contains []string
	}{
		{
			format:   FormatPlainText,
			contains: []string{"Request:", "Options:", "Option A", "Request ID: test-123"},
		},
		{
			format:   FormatJSON,
			contains: []string{`"request_id": "test-123"`, `"agent_name": "FormatAgent"`},
		},
		{
			format:   FormatMarkdown,
			contains: []string{"## Human Input Required", "**Request:**", "1. Option A"},
		},
		{
			format:   FormatHTML,
			contains: []string{"<div class='human-request'>", "<h2>Human Input Required</h2>", "<li>Option A</li>"},
		},
	}

	for _, tt := range testFormats {
		humanAgent := NewHumanAgent(HumanAgentConfig{
			Name:          "FormatTestAgent",
			RequestFormat: tt.format,
		})

		formatted, err := humanAgent.FormatRequest(request)
		if err != nil {
			t.Errorf("Format %d failed: %v", tt.format, err)
			continue
		}

		for _, substring := range tt.contains {
			if !strings.Contains(formatted, substring) {
				t.Errorf("Format %d missing '%s'\nGot: %s", tt.format, substring, formatted)
			}
		}

		t.Logf("✓ Format %d successful", tt.format)
	}
}

// Test 10: Human Approval Tool
func TestHumanApprovalTool(t *testing.T) {
	manager := NewHumanResponseManager()
	approvalTool := CreateHumanApprovalTool(manager, 2*time.Second)

	ctx := context.Background()
	params := map[string]interface{}{
		"instruction": "Approve budget increase",
		"options":     []string{"Approve", "Reject", "Request More Info"},
		"amount":      "$50,000",
	}

	// Background approval
	go func() {
		time.Sleep(100 * time.Millisecond)
		requests := manager.ListPendingRequests()
		if len(requests) > 0 {
			manager.DeliverResponse(&HumanResponse{
				RequestID: requests[0],
				Response:  "Approve",
				Metadata: map[string]interface{}{
					"approver": "CFO",
					"notes":    "Within budget guidelines",
				},
				Timestamp: time.Now(),
			})
		}
	}()

	result, err := approvalTool.Execute(ctx, params)
	if err != nil {
		t.Fatalf("Approval tool failed: %v", err)
	}

	if result != "Approve" {
		t.Errorf("Expected 'Approve', got '%v'", result)
	}

	t.Logf("✓ Human approval tool successful")
	t.Logf("  Result: %v", result)
}

// Test 11: Context Cancellation
func TestHumanAgentContextCancellation(t *testing.T) {
	manager := NewHumanResponseManager()

	humanAgent := NewHumanAgent(HumanAgentConfig{
		Name:            "CancelAgent",
		Timeout:         10 * time.Second, // Long timeout
		ResponseManager: manager,
	})

	ctx, cancel := context.WithCancel(context.Background())

	// Cancel after 200ms
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()

	startTime := time.Now()
	_, err := humanAgent.Execute(ctx, &agent.AgentInput{
		Instruction: "Test cancellation",
	})
	duration := time.Since(startTime)

	if err == nil {
		t.Error("Expected context cancellation error")
	}

	if err != context.Canceled {
		t.Errorf("Expected context.Canceled, got: %v", err)
	}

	if duration > 500*time.Millisecond {
		t.Errorf("Cancellation took too long: %v", duration)
	}

	t.Logf("✓ Context cancellation handled correctly")
	t.Logf("  Duration: %v", duration)
}

// Test 12: Integration with Sequential Workflow (Generator-Critic with Human Review)
func TestHumanAgentInWorkflow(t *testing.T) {
	manager := NewHumanResponseManager()

	// Create workflow: Generator → HumanReview → Publisher
	generator := agent.NewAgent(agent.AgentConfig{
		Name: "ContentGenerator",
		Executor: &mockExecutor{
			result: map[string]interface{}{
				"content": "Generated blog post content",
			},
		},
	})

	humanReview := NewHumanAgent(HumanAgentConfig{
		Name:            "HumanReviewer",
		Description:     "Reviews generated content",
		Timeout:         2 * time.Second,
		ResponseManager: manager,
	})

	publisher := agent.NewAgent(agent.AgentConfig{
		Name: "Publisher",
		Executor: &mockExecutor{
			result: "Published successfully",
		},
	})

	workflow := NewSequentialAgent(SequentialAgentConfig{
		Name:      "ContentWorkflow",
		SubAgents: []agent.Agent{generator, humanReview, publisher},
		PassOutput: true,
	})

	// Simulate human approval
	go func() {
		time.Sleep(200 * time.Millisecond)
		requests := manager.ListPendingRequests()
		if len(requests) > 0 {
			manager.DeliverResponse(&HumanResponse{
				RequestID: requests[0],
				Response:  "Approved for publication",
				Metadata: map[string]interface{}{
					"editor": "Jane Smith",
					"edits":  "Minor grammar fixes",
				},
				Timestamp: time.Now(),
			})
		}
	}()

	ctx := context.Background()
	output, err := workflow.Execute(ctx, &agent.AgentInput{
		Instruction: "Generate and publish blog post",
	})

	if err != nil {
		t.Fatalf("Workflow failed: %v", err)
	}

	if output.Result != "Published successfully" {
		t.Errorf("Expected successful publication, got: %v", output.Result)
	}

	t.Logf("✓ Human agent in workflow successful")
	t.Logf("  Final result: %v", output.Result)
}

// Test 13: Distributed Multi-Stage Approval Workflow
func TestDistributedMultiStageApproval(t *testing.T) {
	manager := NewHumanResponseManager()
	eventBus := events.NewMemoryEventBus()
	defer eventBus.Close()

	// Create multi-stage approval on different partitions
	managerApproval := NewHumanAgent(HumanAgentConfig{
		Name:            "ManagerApproval",
		Description:     "Manager approval stage",
		Timeout:         3 * time.Second,
		ResponseManager: manager,
		EventBus:        eventBus,
		Partition:       "manager-partition",
	})

	directorApproval := NewHumanAgent(HumanAgentConfig{
		Name:            "DirectorApproval",
		Description:     "Director approval stage",
		Timeout:         3 * time.Second,
		ResponseManager: manager,
		EventBus:        eventBus,
		Partition:       "director-partition",
	})

	cfoApproval := NewHumanAgent(HumanAgentConfig{
		Name:            "CFOApproval",
		Description:     "CFO final approval",
		Timeout:         3 * time.Second,
		ResponseManager: manager,
		EventBus:        eventBus,
		Partition:       "cfo-partition",
	})

	// Create sequential approval workflow
	approvalWorkflow := NewSequentialAgent(SequentialAgentConfig{
		Name:      "BudgetApprovalWorkflow",
		SubAgents: []agent.Agent{managerApproval, directorApproval, cfoApproval},
		PassOutput: true,
	})

	// Track approval stages
	approvalStages := []string{}
	var stageLock sync.Mutex

	// Background approval handler
	go func() {
		approvals := []string{"Manager approved", "Director approved", "CFO approved"}
		for i := 0; i < 3; i++ {
			time.Sleep(200 * time.Millisecond)
			requests := manager.ListPendingRequests()
			if len(requests) > 0 {
				requestID := requests[0]
				request, _ := manager.GetPendingRequest(requestID)

				stageLock.Lock()
				approvalStages = append(approvalStages, request.AgentName)
				stageLock.Unlock()

				manager.DeliverResponse(&HumanResponse{
					RequestID: requestID,
					Response:  approvals[i],
					Timestamp: time.Now(),
				})
			}
		}
	}()

	ctx := context.Background()
	output, err := approvalWorkflow.Execute(ctx, &agent.AgentInput{
		Instruction: "Approve $100,000 budget increase",
		Context: map[string]interface{}{
			"amount":     "$100,000",
			"department": "Engineering",
		},
	})

	if err != nil {
		t.Fatalf("Approval workflow failed: %v", err)
	}

	if output.Result != "CFO approved" {
		t.Errorf("Expected 'CFO approved', got: %v", output.Result)
	}

	stageLock.Lock()
	defer stageLock.Unlock()

	if len(approvalStages) != 3 {
		t.Errorf("Expected 3 approval stages, got %d", len(approvalStages))
	}

	expectedStages := []string{"ManagerApproval", "DirectorApproval", "CFOApproval"}
	for i, expected := range expectedStages {
		if i < len(approvalStages) && approvalStages[i] != expected {
			t.Errorf("Stage %d: expected '%s', got '%s'", i, expected, approvalStages[i])
		}
	}

	t.Logf("✓ Distributed multi-stage approval workflow successful")
	t.Logf("  Approval stages: %v", approvalStages)
	t.Logf("  Final result: %v", output.Result)
}

// Test 14: Response Manager Thread Safety
func TestResponseManagerThreadSafety(t *testing.T) {
	manager := NewHumanResponseManager()

	numGoroutines := 50
	var wg sync.WaitGroup

	// Concurrent register/deliver operations
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			request := &HumanRequest{
				RequestID:   fmt.Sprintf("req-%d", idx),
				AgentName:   "ThreadSafetyTest",
				Instruction: fmt.Sprintf("Request %d", idx),
				Timeout:     1 * time.Second,
				Timestamp:   time.Now(),
			}

			// Register
			responseChan := manager.RegisterRequest(request)

			// Immediately deliver response from another "thread"
			go func() {
				time.Sleep(10 * time.Millisecond)
				manager.DeliverResponse(&HumanResponse{
					RequestID: request.RequestID,
					Response:  fmt.Sprintf("Response %d", idx),
					Timestamp: time.Now(),
				})
			}()

			// Wait for response
			select {
			case response := <-responseChan:
				if response.RequestID != request.RequestID {
					t.Errorf("Request ID mismatch: %s != %s", response.RequestID, request.RequestID)
				}
			case <-time.After(2 * time.Second):
				t.Errorf("Timeout waiting for response %d", idx)
			}
		}(i)
	}

	wg.Wait()

	// Verify all requests were cleaned up
	pending := manager.ListPendingRequests()
	if len(pending) != 0 {
		t.Errorf("Expected 0 pending requests, got %d", len(pending))
	}

	t.Logf("✓ Response manager thread safety verified")
	t.Logf("  Concurrent operations: %d", numGoroutines)
}
