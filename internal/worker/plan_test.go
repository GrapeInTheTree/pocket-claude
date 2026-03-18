package worker

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"
)

func TestPlanStateStorage(t *testing.T) {
	pool := NewBackgroundPool(nil, nil, nil, nil, testLogger())

	// No plan initially
	if plan := pool.GetPlan("my-app"); plan != nil {
		t.Error("Expected nil plan")
	}

	// Store a plan
	pool.plans.Store("my-app", &PlanState{
		SessionID:   "sess_123",
		Message:     "add auth",
		PlanText:    "1. Create middleware\n2. Add JWT\n3. Test",
		CreatedAt:   time.Now(),
		ProjectName: "my-app",
	})

	// Retrieve plan
	plan := pool.GetPlan("my-app")
	if plan == nil {
		t.Fatal("Expected plan to exist")
	}
	if plan.SessionID != "sess_123" {
		t.Errorf("SessionID = %q, want 'sess_123'", plan.SessionID)
	}
	if plan.Message != "add auth" {
		t.Errorf("Message = %q, want 'add auth'", plan.Message)
	}

	// Cancel plan
	if !pool.CancelPlan("my-app") {
		t.Error("Expected CancelPlan to return true")
	}
	if plan := pool.GetPlan("my-app"); plan != nil {
		t.Error("Expected plan to be cancelled")
	}

	// Cancel nonexistent
	if pool.CancelPlan("nonexistent") {
		t.Error("Expected CancelPlan to return false for nonexistent")
	}
}

func TestPlanPerProject(t *testing.T) {
	pool := NewBackgroundPool(nil, nil, nil, nil, testLogger())

	pool.plans.Store("project-a", &PlanState{Message: "plan A"})
	pool.plans.Store("project-b", &PlanState{Message: "plan B"})

	planA := pool.GetPlan("project-a")
	planB := pool.GetPlan("project-b")

	if planA == nil || planA.Message != "plan A" {
		t.Error("Expected plan A")
	}
	if planB == nil || planB.Message != "plan B" {
		t.Error("Expected plan B")
	}

	// Cancel A doesn't affect B
	pool.CancelPlan("project-a")
	if pool.GetPlan("project-a") != nil {
		t.Error("Expected plan A to be gone")
	}
	if pool.GetPlan("project-b") == nil {
		t.Error("Expected plan B to remain")
	}
}

func TestExecutePlanNoActivePlan(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	pool := NewBackgroundPool(nil, nil, nil, nil, logger)

	_, err := pool.ExecutePlan(context.Background(), "no-plan-project")
	if err == nil {
		t.Error("Expected error when no plan exists")
	}
	if err.Error() != `no active plan for project "no-plan-project" — use /plan <message> first` {
		t.Errorf("Unexpected error: %v", err)
	}
}
