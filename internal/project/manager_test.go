package project

import (
	"log/slog"
	"os"
	"testing"

	"github.com/GrapeInTheTree/pocket-claude/internal/config"
	"github.com/GrapeInTheTree/pocket-claude/internal/store"
)

func testManager(t *testing.T) *Manager {
	t.Helper()
	dir := t.TempDir()

	cfg := config.Config{
		ProjectsFile:  dir + "/projects.json",
		CLIPath:       "echo",
		CLIWorkDir:    dir,
		CLITimeoutSec: 5,
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	return NewManager(cfg, logger)
}

func TestNewManagerCreatesDefault(t *testing.T) {
	m := testManager(t)

	if m.ActiveProject() == "" {
		t.Error("Expected default active project")
	}

	active, projects := m.ListProjects()
	if len(projects) == 0 {
		t.Fatal("Expected at least 1 project")
	}
	if _, ok := projects[active]; !ok {
		t.Error("Active project not in project list")
	}
}

func TestAddAndRemoveProject(t *testing.T) {
	m := testManager(t)
	dir := t.TempDir()

	if err := m.AddProject("test-project", dir); err != nil {
		t.Fatalf("AddProject: %v", err)
	}

	if !m.HasProject("test-project") {
		t.Error("Expected HasProject to return true")
	}

	// Duplicate
	if err := m.AddProject("test-project", dir); err == nil {
		t.Error("Expected error on duplicate project")
	}

	// Remove
	if err := m.RemoveProject("test-project"); err != nil {
		t.Fatalf("RemoveProject: %v", err)
	}

	if m.HasProject("test-project") {
		t.Error("Expected project to be removed")
	}
}

func TestAddProjectInvalidPath(t *testing.T) {
	m := testManager(t)

	if err := m.AddProject("bad", "/nonexistent/path/xyz"); err == nil {
		t.Error("Expected error for nonexistent path")
	}
}

func TestRemoveActiveProjectFails(t *testing.T) {
	m := testManager(t)
	active := m.ActiveProject()

	if err := m.RemoveProject(active); err == nil {
		t.Error("Expected error removing active project")
	}
}

func TestRemoveDefaultProjectFails(t *testing.T) {
	m := testManager(t)

	if err := m.RemoveProject("default"); err == nil {
		t.Error("Expected error removing default project")
	}
}

func TestSwitchProject(t *testing.T) {
	m := testManager(t)
	dir := t.TempDir()

	m.AddProject("other", dir)

	if err := m.SwitchProject("other"); err != nil {
		t.Fatalf("SwitchProject: %v", err)
	}
	if m.ActiveProject() != "other" {
		t.Errorf("Active = %q, want 'other'", m.ActiveProject())
	}

	// Switch to same (no-op)
	if err := m.SwitchProject("other"); err != nil {
		t.Fatalf("SwitchProject same: %v", err)
	}

	// Switch to nonexistent
	if err := m.SwitchProject("nonexistent"); err == nil {
		t.Error("Expected error switching to nonexistent")
	}
}

func TestRenameProject(t *testing.T) {
	m := testManager(t)
	dir := t.TempDir()

	m.AddProject("old-name", dir)

	if err := m.RenameProject("old-name", "new-name"); err != nil {
		t.Fatalf("RenameProject: %v", err)
	}

	if m.HasProject("old-name") {
		t.Error("Old name should not exist")
	}
	if !m.HasProject("new-name") {
		t.Error("New name should exist")
	}
}

func TestRenameDefaultFails(t *testing.T) {
	m := testManager(t)

	if err := m.RenameProject("default", "other"); err == nil {
		t.Error("Expected error renaming default")
	}
}

func TestRenameToExistingFails(t *testing.T) {
	m := testManager(t)
	dir1, dir2 := t.TempDir(), t.TempDir()

	m.AddProject("proj-a", dir1)
	m.AddProject("proj-b", dir2)

	if err := m.RenameProject("proj-a", "proj-b"); err == nil {
		t.Error("Expected error renaming to existing name")
	}
}

func TestRenameActiveProject(t *testing.T) {
	m := testManager(t)
	dir := t.TempDir()

	m.AddProject("active-proj", dir)
	m.SwitchProject("active-proj")

	m.RenameProject("active-proj", "renamed-proj")

	if m.ActiveProject() != "renamed-proj" {
		t.Errorf("Active = %q, want 'renamed-proj'", m.ActiveProject())
	}
}

func TestNewBackgroundExecutor(t *testing.T) {
	m := testManager(t)

	active := m.ActiveProject()
	exec, err := m.NewBackgroundExecutor(active)
	if err != nil {
		t.Fatalf("NewBackgroundExecutor: %v", err)
	}
	if exec == nil {
		t.Error("Expected non-nil executor")
	}

	// Nonexistent project
	_, err = m.NewBackgroundExecutor("nonexistent")
	if err == nil {
		t.Error("Expected error for nonexistent project")
	}
}

func TestTrackUsageForProject(t *testing.T) {
	m := testManager(t)
	dir := t.TempDir()
	m.AddProject("tracked", dir)
	m.SwitchProject("tracked")

	m.TrackUsageForProject("tracked", &store.CLIResult{TotalCostUSD: 0.05})
	m.TrackUsageForProject("tracked", &store.CLIResult{TotalCostUSD: 0.03})

	u := m.GetUsage() // gets active project usage
	if u.TotalCostUSD < 0.07 || u.TotalCostUSD > 0.09 {
		t.Errorf("TotalCostUSD = %f, want ~0.08", u.TotalCostUSD)
	}
	if u.TotalMessages != 2 {
		t.Errorf("TotalMessages = %d, want 2", u.TotalMessages)
	}
}

func TestTrackUsageForDeletedProject(t *testing.T) {
	m := testManager(t)

	// Should not panic
	m.TrackUsageForProject("nonexistent", &store.CLIResult{TotalCostUSD: 1.0})
	m.TrackUsageForProject("deleted", nil)
}

func TestHasProject(t *testing.T) {
	m := testManager(t)

	active := m.ActiveProject()
	if !m.HasProject(active) {
		t.Error("Expected active project to exist")
	}
	if m.HasProject("no-such-project") {
		t.Error("Expected nonexistent project to not exist")
	}
}

func TestUsageTracking(t *testing.T) {
	m := testManager(t)

	m.TrackUsage(&store.CLIResult{TotalCostUSD: 0.1})
	m.TrackUsage(&store.CLIResult{TotalCostUSD: 0.2})

	u := m.GetUsage()
	if u.TotalMessages != 2 {
		t.Errorf("TotalMessages = %d, want 2", u.TotalMessages)
	}

	m.ResetSessionUsage()
	u = m.GetUsage()
	if u.SessionCostUSD != 0 {
		t.Errorf("SessionCostUSD = %f, want 0 after reset", u.SessionCostUSD)
	}
	// Total should not be reset
	if u.TotalCostUSD < 0.29 {
		t.Errorf("TotalCostUSD = %f, want ~0.3", u.TotalCostUSD)
	}
}

func TestPersistenceAcrossLoads(t *testing.T) {
	dir := t.TempDir()
	projectDir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	cfg := config.Config{
		ProjectsFile:  dir + "/projects.json",
		CLIPath:       "echo",
		CLIWorkDir:    dir,
		CLITimeoutSec: 5,
	}

	// Create manager, add project
	m1 := NewManager(cfg, logger)
	m1.AddProject("persisted", projectDir)
	m1.SwitchProject("persisted")

	// Create new manager from same file
	m2 := NewManager(cfg, logger)

	if !m2.HasProject("persisted") {
		t.Error("Expected persisted project to survive reload")
	}
	if m2.ActiveProject() != "persisted" {
		t.Errorf("Active = %q, want 'persisted'", m2.ActiveProject())
	}
}
