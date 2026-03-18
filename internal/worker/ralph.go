package worker

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/GrapeInTheTree/pocket-claude/internal/store"
)

const (
	defaultMaxIterations = 5
	maxAllowedIterations = 20
	defaultMaxCostUSD    = 1.0
	stallThreshold       = 3
)

// SubmitRalph starts an iterative autonomous loop as a background task.
func (bp *BackgroundPool) SubmitRalph(ctx context.Context, projectName, message string, maxIter int) (string, error) {
	if maxIter <= 0 {
		maxIter = defaultMaxIterations
	}
	if maxIter > maxAllowedIterations {
		maxIter = maxAllowedIterations
	}

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
		ID:            taskID,
		Project:       projectName,
		Message:       message,
		State:         "running",
		StartedAt:     time.Now(),
		Cancel:        cancel,
		IsRalph:       true,
		MaxIterations: maxIter,
		MaxCostUSD:    defaultMaxCostUSD,
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
		bp.runRalph(taskCtx, task)
	}()

	return taskID, nil
}

func (bp *BackgroundPool) runRalph(ctx context.Context, task *BackgroundTask) {
	bp.logger.Info("Ralph loop started", "id", task.ID, "project", task.Project, "max_iterations", task.MaxIterations)

	exec, err := bp.projects.NewBackgroundExecutor(task.Project)
	if err != nil {
		bp.setTaskState(task.ID, "failed", err.Error())
		bp.sendFn(fmt.Sprintf("❌ Ralph Failed\n🆔 %s\nError: %s", task.ID, err.Error()))
		return
	}

	var lastResult *store.CLIResult

	for iteration := 1; iteration <= task.MaxIterations; iteration++ {
		if ctx.Err() != nil {
			bp.setTaskState(task.ID, "cancelled", "")
			bp.logger.Info("Ralph cancelled", "id", task.ID, "iteration", iteration)
			return
		}

		bp.mu.Lock()
		task.CurrentIteration = iteration
		bp.mu.Unlock()

		// Build prompt
		var prompt string
		if iteration == 1 {
			prompt = task.Message
		} else {
			prompt = "Continue working on the task. Check your previous work and continue from where you left off. If the task is fully complete, respond with RALPH_DONE followed by a brief summary."
		}

		// Typing indicator
		typingCtx, stopTyping := context.WithCancel(ctx)
		go bp.sendTypingFn(typingCtx)

		// Execute
		result, err := exec.Execute(ctx, prompt, false)
		stopTyping()

		if err != nil {
			if ctx.Err() != nil {
				bp.setTaskState(task.ID, "cancelled", "")
				return
			}
			bp.setTaskState(task.ID, "failed", err.Error())
			bp.sendFn(fmt.Sprintf("❌ Ralph Failed at iteration %d/%d\n🆔 %s\n\n%s",
				iteration, task.MaxIterations, task.ID, Truncate(err.Error(), 200)))
			return
		}

		// Permission approval
		if len(result.PermissionDenials) > 0 {
			bp.setTaskState(task.ID, "approval", "")
			approved, approvalErr := bp.requestApproval(ctx, task.ID, result)
			if approvalErr != nil || !approved {
				bp.setTaskState(task.ID, "done", "")
				reason := "permission denied"
				if approvalErr != nil {
					reason = approvalErr.Error()
				}
				bp.sendRalphResult(task, result, reason)
				return
			}
			bp.setTaskState(task.ID, "running", "")

			typingCtx2, stopTyping2 := context.WithCancel(ctx)
			go bp.sendTypingFn(typingCtx2)
			result, err = exec.Execute(ctx, prompt, true)
			stopTyping2()
			if err != nil {
				if ctx.Err() != nil {
					bp.setTaskState(task.ID, "cancelled", "")
					return
				}
				bp.setTaskState(task.ID, "failed", err.Error())
				bp.sendFn(fmt.Sprintf("❌ Ralph Failed at iteration %d/%d\n🆔 %s\n\n%s",
					iteration, task.MaxIterations, task.ID, Truncate(err.Error(), 200)))
				return
			}
		}

		lastResult = result

		// Track cost + stall detection + completion (all under lock)
		bp.projects.TrackUsageForProject(task.Project, result)

		bp.mu.Lock()
		task.TotalCostUSD += result.TotalCostUSD
		totalCost := task.TotalCostUSD
		maxCost := task.MaxCostUSD

		// Stall detection
		resultLen := len([]rune(result.Result))
		if abs(resultLen-task.LastResultLen) < 50 && len(result.ToolSummary) == 0 {
			task.StallCount++
		} else {
			task.StallCount = 0
		}
		task.LastResultLen = resultLen
		stallCount := task.StallCount
		iterNum := task.CurrentIteration
		bp.mu.Unlock()

		// Completion check
		if isRalphComplete(result) {
			bp.mu.Lock()
			task.ResultText = result.Result
			bp.mu.Unlock()
			bp.setTaskState(task.ID, "done", "")
			bp.sendRalphResult(task, result, "completed")
			return
		}

		if stallCount >= stallThreshold {
			bp.mu.Lock()
			task.ResultText = result.Result
			bp.mu.Unlock()
			bp.setTaskState(task.ID, "done", "")
			bp.sendRalphResult(task, result, fmt.Sprintf("stalled (%d iterations without progress)", stallThreshold))
			return
		}

		// Cost circuit breaker
		if maxCost > 0 && totalCost > maxCost {
			bp.mu.Lock()
			task.ResultText = result.Result
			bp.mu.Unlock()
			bp.setTaskState(task.ID, "done", "")
			bp.sendRalphResult(task, result, fmt.Sprintf("cost limit $%.2f exceeded", maxCost))
			return
		}

		// Progress update (use local copies)
		summary := Truncate(result.Result, 100)
		bp.sendFn(fmt.Sprintf("🔁 Ralph [%s] Iteration %d/%d\n📂 %s | 💰 $%.4f\n\n%s",
			task.ID, iterNum, task.MaxIterations, task.Project, totalCost, summary))
	}

	// Max iterations reached
	if lastResult != nil {
		bp.mu.Lock()
		task.ResultText = lastResult.Result
		bp.mu.Unlock()
	}
	bp.setTaskState(task.ID, "done", "")
	bp.sendRalphResult(task, lastResult, "max iterations reached")
}

func isRalphComplete(result *store.CLIResult) bool {
	if len(result.ToolSummary) == 0 {
		return true
	}
	if strings.Contains(result.Result, "RALPH_DONE") {
		return true
	}
	return false
}

func (bp *BackgroundPool) sendRalphResult(task *BackgroundTask, result *store.CLIResult, reason string) {
	elapsed := time.Since(task.StartedAt)
	var elapsedStr string
	if elapsed < time.Minute {
		elapsedStr = fmt.Sprintf("%.0fs", elapsed.Seconds())
	} else {
		elapsedStr = fmt.Sprintf("%dm%ds", int(elapsed.Minutes()), int(elapsed.Seconds())%60)
	}

	response := "(no response)"
	if result != nil && result.Result != "" {
		response = result.Result
	}

	// Read task fields under lock
	bp.mu.Lock()
	currentIter := task.CurrentIteration
	maxIter := task.MaxIterations
	totalCost := task.TotalCostUSD
	bp.mu.Unlock()

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("✅ Ralph Done (%d/%d iterations)\n", currentIter, maxIter))
	sb.WriteString(fmt.Sprintf("📂 %s | 💰 $%.4f | ⏱ %s\n", task.Project, totalCost, elapsedStr))
	sb.WriteString(fmt.Sprintf("🏁 %s\n\n", reason))
	sb.WriteString(response)

	if err := bp.sendFn(sb.String()); err != nil {
		bp.logger.Error("Failed to send ralph result", "id", task.ID, "error", err)
	}
	bp.logger.Info("Ralph completed", "id", task.ID, "iterations", task.CurrentIteration, "reason", reason)
}

// RalphStatus returns formatted status of running ralph tasks.
func (bp *BackgroundPool) RalphStatus() string {
	bp.mu.Lock()
	defer bp.mu.Unlock()

	var active []struct {
		id, project, message, state string
		iteration, maxIter          int
		cost                        float64
		elapsed                     time.Duration
	}

	for _, t := range bp.tasks {
		if t.IsRalph && (t.State == "running" || t.State == "approval") {
			active = append(active, struct {
				id, project, message, state string
				iteration, maxIter          int
				cost                        float64
				elapsed                     time.Duration
			}{t.ID, t.Project, t.Message, t.State, t.CurrentIteration, t.MaxIterations, t.TotalCostUSD, time.Since(t.StartedAt)})
		}
	}

	if len(active) == 0 {
		return "No active ralph loops."
	}

	var sb strings.Builder
	sb.WriteString("🔁 Ralph Loops\n\n")

	for _, a := range active {
		var elapsedStr string
		if a.elapsed < time.Minute {
			elapsedStr = fmt.Sprintf("%.0fs", a.elapsed.Seconds())
		} else {
			elapsedStr = fmt.Sprintf("%dm%ds", int(a.elapsed.Minutes()), int(a.elapsed.Seconds())%60)
		}
		stateIcon := "🔁"
		if a.state == "approval" {
			stateIcon = "🔐"
		}
		sb.WriteString(fmt.Sprintf("%s %s [%d/%d] %s\n", stateIcon, a.id, a.iteration, a.maxIter, Truncate(a.message, 30)))
		sb.WriteString(fmt.Sprintf("   📂 %s | 💰 $%.4f | ⏱ %s\n\n", a.project, a.cost, elapsedStr))
	}

	return sb.String()
}

// ParseRalphArgs extracts message and max iterations from ralph command arguments.
func ParseRalphArgs(args string) (message string, maxIter int) {
	maxIter = defaultMaxIterations
	if idx := strings.Index(args, "--max "); idx >= 0 {
		message = strings.TrimSpace(args[:idx])
		rest := strings.TrimSpace(args[idx+6:])
		fields := strings.Fields(rest)
		if len(fields) > 0 {
			if n, err := strconv.Atoi(fields[0]); err == nil && n > 0 && n <= maxAllowedIterations {
				maxIter = n
			}
		}
	} else {
		message = args
	}
	return
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
