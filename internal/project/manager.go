package project

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/GrapeInTheTree/pocket-claude/internal/claude"
	"github.com/GrapeInTheTree/pocket-claude/internal/config"
	"github.com/GrapeInTheTree/pocket-claude/internal/store"
)

// Manager owns per-project executors and routes calls to the active one.
type Manager struct {
	mu       sync.Mutex
	active   string
	projects map[string]ProjectConfig
	execs    map[string]*claude.Executor
	usage    map[string]*ProjectUsage

	filePath     string
	cliPath      string
	timeout      time.Duration
	systemPrompt string
	model        string
	defaultDirs  []string
	logger       *slog.Logger
}

// NewManager loads projects.json or creates a default from config.
func NewManager(cfg config.Config, logger *slog.Logger) *Manager {
	var defaultDirs []string
	if cfg.CLIAddDirs != "" {
		for _, d := range strings.Split(cfg.CLIAddDirs, ",") {
			d = strings.TrimSpace(d)
			if d != "" {
				defaultDirs = append(defaultDirs, d)
			}
		}
	}

	workDir := cfg.CLIWorkDir
	if workDir == "" {
		workDir = "."
	}

	m := &Manager{
		filePath:     cfg.ProjectsFile,
		cliPath:      cfg.CLIPath,
		timeout:      time.Duration(cfg.CLITimeoutSec) * time.Second,
		systemPrompt: cfg.CLISystemPrompt,
		model:        cfg.CLIModel,
		defaultDirs:  defaultDirs,
		logger:       logger,
		execs:        make(map[string]*claude.Executor),
		usage:        make(map[string]*ProjectUsage),
	}

	if err := m.load(); err != nil {
		logger.Warn("Failed to load projects file, creating default", "error", err)
		pf := NewProjectsFile(workDir, defaultDirs)
		m.active = pf.Active
		m.projects = pf.Projects
		m.persist()
	}

	// Create executors for all projects
	for name, pc := range m.projects {
		m.execs[name] = claude.NewProjectExecutor(
			m.cliPath, pc.WorkDir, pc.AddDirs, m.timeout, m.systemPrompt, m.model, logger,
		)
		m.usage[name] = &ProjectUsage{}
	}

	logger.Info("Project manager initialized", "active", m.active, "count", len(m.projects))
	return m
}

func (m *Manager) load() error {
	data, err := os.ReadFile(m.filePath)
	if err != nil {
		return err
	}

	var pf ProjectsFile
	if err := json.Unmarshal(data, &pf); err != nil {
		return err
	}

	if len(pf.Projects) == 0 {
		return fmt.Errorf("empty projects file")
	}

	// Ensure active project exists in the map
	if _, ok := pf.Projects[pf.Active]; !ok {
		// Pick first available project
		for name := range pf.Projects {
			pf.Active = name
			break
		}
	}

	m.active = pf.Active
	m.projects = pf.Projects
	return nil
}

func (m *Manager) persist() {
	pf := ProjectsFile{
		Active:   m.active,
		Projects: m.projects,
	}
	data, err := json.MarshalIndent(pf, "", "  ")
	if err != nil {
		m.logger.Error("Failed to marshal projects", "error", err)
		return
	}
	if err := os.WriteFile(m.filePath, data, 0644); err != nil {
		m.logger.Error("Failed to write projects file", "error", err)
	}
}

// ActiveProject returns the name of the active project.
func (m *Manager) ActiveProject() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.active
}

// ListProjects returns all project configs.
func (m *Manager) ListProjects() (active string, projects map[string]ProjectConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make(map[string]ProjectConfig, len(m.projects))
	for k, v := range m.projects {
		cp[k] = v
	}
	return m.active, cp
}

// SwitchProject changes the active project and resets session for the new project.
func (m *Manager) SwitchProject(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.projects[name]; !ok {
		return fmt.Errorf("project %q not found", name)
	}
	if name == m.active {
		return nil // already active
	}
	m.active = name
	m.persist()
	// Auto-reset session cost for the newly switched project
	m.usage[name].SessionCostUSD = 0
	m.logger.Info("Switched project", "project", name)
	return nil
}

// AddProject adds a new project. Returns error if it already exists or path doesn't exist.
func (m *Manager) AddProject(name, workDir string) error {
	// Validate path exists before acquiring lock
	info, err := os.Stat(workDir)
	if err != nil {
		return fmt.Errorf("path does not exist: %s", workDir)
	}
	if !info.IsDir() {
		return fmt.Errorf("path is not a directory: %s", workDir)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.projects[name]; ok {
		return fmt.Errorf("project %q already exists", name)
	}

	pc := ProjectConfig{
		Name:      name,
		WorkDir:   workDir,
		AddDirs:   m.defaultDirs,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	m.projects[name] = pc
	m.execs[name] = claude.NewProjectExecutor(
		m.cliPath, pc.WorkDir, pc.AddDirs, m.timeout, m.systemPrompt, m.model, m.logger,
	)
	m.usage[name] = &ProjectUsage{}
	m.persist()
	m.logger.Info("Added project", "name", name, "work_dir", workDir)
	return nil
}

// RemoveProject removes a project. Cannot remove the active or default project.
func (m *Manager) RemoveProject(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if name == "default" {
		return fmt.Errorf("cannot remove default project")
	}
	if name == m.active {
		return fmt.Errorf("cannot remove active project — switch first")
	}
	if _, ok := m.projects[name]; !ok {
		return fmt.Errorf("project %q not found", name)
	}

	delete(m.projects, name)
	delete(m.execs, name)
	delete(m.usage, name)
	m.persist()
	m.logger.Info("Removed project", "name", name)
	return nil
}

// RenameProject renames a project. Cannot rename "default".
func (m *Manager) RenameProject(oldName, newName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if oldName == "default" {
		return fmt.Errorf("cannot rename default project")
	}
	pc, ok := m.projects[oldName]
	if !ok {
		return fmt.Errorf("project %q not found", oldName)
	}
	if _, exists := m.projects[newName]; exists {
		return fmt.Errorf("project %q already exists", newName)
	}

	pc.Name = newName
	m.projects[newName] = pc
	m.execs[newName] = m.execs[oldName]
	m.usage[newName] = m.usage[oldName]
	delete(m.projects, oldName)
	delete(m.execs, oldName)
	delete(m.usage, oldName)

	if m.active == oldName {
		m.active = newName
	}
	m.persist()
	m.logger.Info("Renamed project", "old", oldName, "new", newName)
	return nil
}

// --- Delegation to active executor ---

func (m *Manager) activeExec() *claude.Executor {
	// Caller must hold m.mu
	return m.execs[m.active]
}

// Execute delegates to the active project's executor.
func (m *Manager) Execute(ctx context.Context, userMessage string, skipPermissions bool) (*store.CLIResult, error) {
	m.mu.Lock()
	exec := m.activeExec()
	m.mu.Unlock()
	return exec.Execute(ctx, userMessage, skipPermissions)
}

// ResetSession resets the active project's session.
func (m *Manager) ResetSession() {
	m.mu.Lock()
	exec := m.activeExec()
	m.mu.Unlock()
	exec.ResetSession()
}

// SetResumeID sets the resume ID on the active executor.
func (m *Manager) SetResumeID(id string) {
	m.mu.Lock()
	exec := m.activeExec()
	m.mu.Unlock()
	exec.SetResumeID(id)
}

// GetSessions returns sessions for the active project.
func (m *Manager) GetSessions() []claude.SessionInfo {
	m.mu.Lock()
	exec := m.activeExec()
	m.mu.Unlock()
	return exec.GetSessions()
}

// SetSessionName sets the name on the active executor's session.
func (m *Manager) SetSessionName(name string) {
	m.mu.Lock()
	exec := m.activeExec()
	m.mu.Unlock()
	exec.SetSessionName(name)
}

// GetCurrentSessionID returns the active executor's session ID.
func (m *Manager) GetCurrentSessionID() string {
	m.mu.Lock()
	exec := m.activeExec()
	m.mu.Unlock()
	return exec.GetCurrentSessionID()
}

// SetModel sets the model on the active executor.
func (m *Manager) SetModel(model string) {
	m.mu.Lock()
	exec := m.activeExec()
	m.mu.Unlock()
	exec.SetModel(model)
}

// GetModel returns the active executor's model.
func (m *Manager) GetModel() string {
	m.mu.Lock()
	exec := m.activeExec()
	m.mu.Unlock()
	return exec.GetModel()
}

// NewBackgroundExecutor creates an independent Executor for the named project.
// It is NOT stored in the Manager's map — the caller owns its lifecycle.
func (m *Manager) NewBackgroundExecutor(projectName string) (*claude.Executor, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	pc, ok := m.projects[projectName]
	if !ok {
		return nil, fmt.Errorf("project %q not found", projectName)
	}

	return claude.NewProjectExecutor(
		m.cliPath, pc.WorkDir, pc.AddDirs, m.timeout, m.systemPrompt, m.model, m.logger,
	), nil
}

// TrackUsageForProject records cost for a specific (possibly non-active) project.
// Silently ignores unknown projects (e.g. deleted while bg task was running).
func (m *Manager) TrackUsageForProject(projectName string, result *store.CLIResult) {
	if result == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.usage[projectName]
	if !ok {
		return // project was deleted, ignore
	}
	u.TotalCostUSD += result.TotalCostUSD
	u.SessionCostUSD += result.TotalCostUSD
	u.TotalMessages++
}

// HasProject returns whether a project with the given name exists.
func (m *Manager) HasProject(name string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.projects[name]
	return ok
}

// --- Usage tracking ---

// TrackUsage records cost from a CLI result for the active project.
func (m *Manager) TrackUsage(result *store.CLIResult) {
	if result == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	u := m.usage[m.active]
	u.TotalCostUSD += result.TotalCostUSD
	u.SessionCostUSD += result.TotalCostUSD
	u.TotalMessages++
}

// GetUsage returns usage stats for the active project.
func (m *Manager) GetUsage() *ProjectUsage {
	m.mu.Lock()
	defer m.mu.Unlock()
	u := m.usage[m.active]
	cp := *u
	return &cp
}

// ResetSessionUsage resets the per-session cost for the active project.
func (m *Manager) ResetSessionUsage() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.usage[m.active].SessionCostUSD = 0
}

// GetProjectInfo returns config and usage for the active project.
func (m *Manager) GetProjectInfo() (ProjectConfig, *ProjectUsage, int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	pc := m.projects[m.active]
	u := *m.usage[m.active]
	sessions := len(m.execs[m.active].GetSessions())
	return pc, &u, sessions
}

// GetTotalUsage returns aggregated usage across all projects.
func (m *Manager) GetTotalUsage() (totalCost float64, totalMessages int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, u := range m.usage {
		totalCost += u.TotalCostUSD
		totalMessages += u.TotalMessages
	}
	return
}

// SearchRepos searches for git repositories under baseDir matching the keyword.
// Returns up to maxResults directory paths.
func SearchRepos(keyword string, maxResults int) []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}

	keyword = strings.ToLower(keyword)
	var results []string

	// Search up to depth 3 under home directory
	entries, err := os.ReadDir(home)
	if err != nil {
		return nil
	}

	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		searchDir(filepath.Join(home, e.Name()), keyword, 2, &results, maxResults)
		if len(results) >= maxResults {
			break
		}
	}
	return results
}

func searchDir(dir, keyword string, depth int, results *[]string, max int) {
	if len(*results) >= max || depth < 0 {
		return
	}

	base := strings.ToLower(filepath.Base(dir))
	if strings.Contains(base, keyword) {
		// Check if it's a git repo
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			*results = append(*results, dir)
			return
		}
	}

	if depth == 0 {
		return
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		searchDir(filepath.Join(dir, e.Name()), keyword, depth-1, results, max)
		if len(*results) >= max {
			return
		}
	}
}
