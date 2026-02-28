package planner

import (
	"context"
	"testing"
	"time"

	"github.com/seyi/dagens/pkg/agent"
)

func TestNewDefaultPlanner(t *testing.T) {
	agentRegistry := make(map[string]agent.Agent)
	planner := NewDefaultPlanner(agentRegistry, nil)

	if planner == nil {
		t.Fatal("Planner should not be nil")
	}
}

func TestCreateSimplePlan(t *testing.T) {
	planner := NewDefaultPlanner(make(map[string]agent.Agent), nil)
	ctx := context.Background()

	constraints := PlanConstraints{
		MaxSteps:       5,
		MaxParallelism: 2,
		Timeout:        30 * time.Second,
	}

	plan, err := planner.CreatePlan(ctx, "Test objective", constraints)
	if err != nil {
		t.Fatalf("Failed to create plan: %v", err)
	}

	if plan == nil {
		t.Fatal("Plan should not be nil")
	}

	if plan.Objective != "Test objective" {
		t.Errorf("Expected objective 'Test objective', got '%s'", plan.Objective)
	}

	if len(plan.Steps) == 0 {
		t.Error("Plan should have at least one step")
	}
}

func TestValidatePlan(t *testing.T) {
	planner := NewDefaultPlanner(make(map[string]agent.Agent), nil)

	t.Run("Valid Plan", func(t *testing.T) {
		plan := &Plan{
			ID:        "plan-1",
			Objective: "Test",
			Steps: []*PlanStep{
				{
					ID:           "step-1",
					Description:  "Step 1",
					Dependencies: []string{},
				},
			},
		}

		err := planner.ValidatePlan(plan)
		if err != nil {
			t.Errorf("Valid plan should pass validation: %v", err)
		}
	})

	t.Run("Nil Plan", func(t *testing.T) {
		err := planner.ValidatePlan(nil)
		if err == nil {
			t.Error("Nil plan should fail validation")
		}
	})

	t.Run("Empty Objective", func(t *testing.T) {
		plan := &Plan{
			ID:        "plan-1",
			Objective: "",
			Steps:     []*PlanStep{},
		}

		err := planner.ValidatePlan(plan)
		if err == nil {
			t.Error("Plan with empty objective should fail validation")
		}
	})
}

func TestCircularDependencyDetection(t *testing.T) {
	planner := NewDefaultPlanner(make(map[string]agent.Agent), nil)

	plan := &Plan{
		ID:        "plan-1",
		Objective: "Test",
		Steps: []*PlanStep{
			{
				ID:           "step-1",
				Description:  "Step 1",
				Dependencies: []string{"step-2"},
			},
			{
				ID:           "step-2",
				Description:  "Step 2",
				Dependencies: []string{"step-1"},
			},
		},
	}

	err := planner.ValidatePlan(plan)
	if err == nil {
		t.Error("Circular dependency should be detected")
	}
}

func TestOptimizePlan(t *testing.T) {
	planner := NewDefaultPlanner(make(map[string]agent.Agent), nil)

	plan := &Plan{
		ID:        "plan-1",
		Objective: "Test",
		Steps: []*PlanStep{
			{
				ID:           "step-1",
				Description:  "Step 1",
				Dependencies: []string{},
				Timeout:      10 * time.Second,
			},
			{
				ID:           "step-2",
				Description:  "Step 2",
				Dependencies: []string{"step-1"},
				Timeout:      10 * time.Second,
			},
		},
	}

	optimized, err := planner.OptimizePlan(plan)
	if err != nil {
		t.Fatalf("Failed to optimize plan: %v", err)
	}

	if optimized == nil {
		t.Fatal("Optimized plan should not be nil")
	}

	if len(optimized.Steps) != len(plan.Steps) {
		t.Error("Optimized plan should have same number of steps")
	}
}
