package worker

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/GrapeInTheTree/pocket-claude/internal/claude"
)

// PlanState holds the active plan for a project.
type PlanState struct {
	SessionID   string
	Message     string
	PlanText    string
	CreatedAt   time.Time
	ProjectName string
}

// SubmitPlan creates a plan using read-only tools as a background task.
func (bp *BackgroundPool) SubmitPlan(ctx context.Context, projectName, message string) (string, error) {
	bp.mu.Lock()
	if bp.closed {
		bp.mu.Unlock()
		return "", fmt.Errorf("background pool is shutting down")
	}
	bp.mu.Unlock()

	select {
	case bp.sem <- struct{}{}:
	default:
		return "", fmt.Errorf("all %d background slots are busy", maxBackgroundSlots)
	}

	taskID := fmt.Sprintf("bg_%d", taskCounter.Add(1))
	taskCtx, cancel := context.WithCancel(ctx)
	task := &BackgroundTask{
		ID:        taskID,
		Project:   projectName,
		Message:   message,
		State:     "running",
		StartedAt: time.Now(),
		Cancel:    cancel,
	}

	bp.mu.Lock()
	if bp.closed {
		bp.mu.Unlock()
		cancel()
		<-bp.sem
		return "", fmt.Errorf("background pool is shutting down")
	}
	bp.tasks[taskID] = task
	bp.mu.Unlock()

	bp.wg.Add(1)
	go func() {
		defer func() {
			<-bp.sem
			bp.wg.Done()
		}()
		bp.runPlan(taskCtx, task)
	}()

	return taskID, nil
}

func (bp *BackgroundPool) runPlan(ctx context.Context, task *BackgroundTask) {
	bp.logger.Info("Plan creation started", "id", task.ID, "project", task.Project)

	exec, err := bp.projects.NewBackgroundExecutor(task.Project)
	if err != nil {
		bp.setTaskState(task.ID, "failed", err.Error())
		bp.sendFn(fmt.Sprintf("❌ Plan Failed\n🆔 %s\nError: %s", task.ID, err.Error()))
		return
	}

	typingCtx, stopTyping := context.WithCancel(ctx)
	go bp.sendTypingFn(typingCtx)
	defer stopTyping()

	planPrompt := fmt.Sprintf(
		"Create a detailed implementation plan for the following task. "+
			"Analyze the codebase, understand the requirements, and outline specific steps with file paths. "+
			"Do NOT execute anything — only plan.\n\nTask: %s", task.Message)

	opts := claude.ExecuteOptions{
		AllowedTools: []string{"Read", "Glob", "Grep", "WebSearch", "WebFetch"},
	}
	result, err := exec.ExecuteWithOptions(ctx, planPrompt, opts)
	if err != nil {
		if ctx.Err() != nil {
			bp.setTaskState(task.ID, "cancelled", "")
			return
		}
		bp.setTaskState(task.ID, "failed", err.Error())
		bp.sendFn(fmt.Sprintf("❌ Plan Failed\n📂 %s\n🆔 %s\n\n%s", task.Project, task.ID, Truncate(err.Error(), 200)))
		return
	}

	// Permission handling (shouldn't trigger with read-only tools, but safety)
	if len(result.PermissionDenials) > 0 {
		bp.setTaskState(task.ID, "approval", "")
		stopTyping()
		approved, approvalErr := bp.requestApproval(ctx, task.ID, result)
		if approvalErr != nil || !approved {
			bp.setTaskState(task.ID, "done", "")
			bp.sendResult(task, result)
			return
		}
		bp.setTaskState(task.ID, "running", "")
		typingCtx2, stopTyping2 := context.WithCancel(ctx)
		go bp.sendTypingFn(typingCtx2)
		defer stopTyping2()
		result, err = exec.ExecuteWithOptions(ctx, planPrompt, claude.ExecuteOptions{SkipPermissions: true, AllowedTools: opts.AllowedTools})
		if err != nil {
			if ctx.Err() != nil {
				bp.setTaskState(task.ID, "cancelled", "")
				return
			}
			bp.setTaskState(task.ID, "failed", err.Error())
			bp.sendFn(fmt.Sprintf("❌ Plan Failed\n🆔 %s\n\n%s", task.ID, Truncate(err.Error(), 200)))
			return
		}
	}

	// Store the plan
	sessionID := exec.GetCurrentSessionID()
	bp.plans.Store(task.Project, &PlanState{
		SessionID:   sessionID,
		Message:     task.Message,
		PlanText:    result.Result,
		CreatedAt:   time.Now(),
		ProjectName: task.Project,
	})

	bp.projects.TrackUsageForProject(task.Project, result)
	bp.mu.Lock()
	task.ResultText = result.Result
	bp.mu.Unlock()
	bp.setTaskState(task.ID, "done", "")

	// Send plan to user
	var sb strings.Builder
	sb.WriteString("📋 Plan Ready\n")
	sb.WriteString(fmt.Sprintf("📂 %s\n", task.Project))
	if summary := buildToolSummary(result); summary != "" {
		sb.WriteString(summary + "\n")
	}
	sb.WriteString("\n" + result.Result)
	sb.WriteString("\n\n`/plan execute` — Run this plan\n`/plan cancel` — Discard")

	if err := bp.sendFn(sb.String()); err != nil {
		bp.logger.Error("Failed to send plan", "id", task.ID, "error", err)
	}
	bp.logger.Info("Plan created", "id", task.ID, "session", sessionID)
}

// ExecutePlan runs the previously created plan as a background task.
func (bp *BackgroundPool) ExecutePlan(ctx context.Context, projectName string) (string, error) {
	val, ok := bp.plans.Load(projectName)
	if !ok {
		return "", fmt.Errorf("no active plan for project %q — use /plan <message> first", projectName)
	}
	plan := val.(*PlanState)

	bp.mu.Lock()
	if bp.closed {
		bp.mu.Unlock()
		return "", fmt.Errorf("background pool is shutting down")
	}
	bp.mu.Unlock()

	select {
	case bp.sem <- struct{}{}:
	default:
		return "", fmt.Errorf("all %d background slots are busy", maxBackgroundSlots)
	}

	taskID := fmt.Sprintf("bg_%d", taskCounter.Add(1))
	taskCtx, cancel := context.WithCancel(ctx)
	task := &BackgroundTask{
		ID:        taskID,
		Project:   projectName,
		Message:   "Execute plan: " + Truncate(plan.Message, 60),
		State:     "running",
		StartedAt: time.Now(),
		Cancel:    cancel,
	}

	bp.mu.Lock()
	if bp.closed {
		bp.mu.Unlock()
		cancel()
		<-bp.sem
		return "", fmt.Errorf("background pool is shutting down")
	}
	bp.tasks[taskID] = task
	bp.mu.Unlock()

	planCopy := *plan
	bp.plans.Delete(projectName)

	bp.wg.Add(1)
	go func() {
		defer func() {
			<-bp.sem
			bp.wg.Done()
		}()
		bp.runPlanExecution(taskCtx, task, &planCopy)
	}()

	return taskID, nil
}

func (bp *BackgroundPool) runPlanExecution(ctx context.Context, task *BackgroundTask, plan *PlanState) {
	bp.logger.Info("Plan execution started", "id", task.ID, "project", task.Project, "plan_session", plan.SessionID)

	exec, err := bp.projects.NewBackgroundExecutor(task.Project)
	if err != nil {
		bp.setTaskState(task.ID, "failed", err.Error())
		bp.sendFn(fmt.Sprintf("❌ Plan Execution Failed\n🆔 %s\nError: %s", task.ID, err.Error()))
		return
	}

	// Resume the planning session so Claude has full context of its plan
	exec.SetResumeID(plan.SessionID)

	typingCtx, stopTyping := context.WithCancel(ctx)
	go bp.sendTypingFn(typingCtx)
	defer stopTyping()

	result, err := exec.Execute(ctx, "Execute the plan you created. Proceed step by step.", false)
	if err != nil {
		if ctx.Err() != nil {
			bp.setTaskState(task.ID, "cancelled", "")
			return
		}
		bp.setTaskState(task.ID, "failed", err.Error())
		bp.sendFn(fmt.Sprintf("❌ Plan Execution Failed\n📂 %s\n🆔 %s\n\n%s", task.Project, task.ID, Truncate(err.Error(), 200)))
		return
	}

	// Permission approval
	if len(result.PermissionDenials) > 0 {
		bp.setTaskState(task.ID, "approval", "")
		stopTyping()
		approved, approvalErr := bp.requestApproval(ctx, task.ID, result)
		if approvalErr != nil || !approved {
			bp.setTaskState(task.ID, "done", "")
			bp.sendResult(task, result)
			return
		}
		bp.setTaskState(task.ID, "running", "")
		typingCtx2, stopTyping2 := context.WithCancel(ctx)
		go bp.sendTypingFn(typingCtx2)
		defer stopTyping2()
		result, err = exec.Execute(ctx, "Execute the plan you created. Proceed step by step.", true)
		if err != nil {
			if ctx.Err() != nil {
				bp.setTaskState(task.ID, "cancelled", "")
				return
			}
			bp.setTaskState(task.ID, "failed", err.Error())
			bp.sendFn(fmt.Sprintf("❌ Plan Execution Failed\n🆔 %s\n\n%s", task.ID, Truncate(err.Error(), 200)))
			return
		}
	}

	bp.projects.TrackUsageForProject(task.Project, result)
	bp.setTaskState(task.ID, "done", "")
	bp.sendResult(task, result)
}

// CancelPlan discards the active plan for a project.
func (bp *BackgroundPool) CancelPlan(projectName string) bool {
	_, ok := bp.plans.LoadAndDelete(projectName)
	return ok
}

// GetPlan returns the active plan for a project, or nil.
func (bp *BackgroundPool) GetPlan(projectName string) *PlanState {
	val, ok := bp.plans.Load(projectName)
	if !ok {
		return nil
	}
	return val.(*PlanState)
}
