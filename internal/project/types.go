package project

import "time"

// ProjectConfig holds the configuration for a single project.
type ProjectConfig struct {
	Name      string   `json:"name"`
	WorkDir   string   `json:"work_dir"`
	AddDirs   []string `json:"add_dirs,omitempty"`
	CreatedAt string   `json:"created_at"`
}

// ProjectsFile is the JSON schema for projects.json persistence.
type ProjectsFile struct {
	Active   string                   `json:"active"`
	Projects map[string]ProjectConfig `json:"projects"`
}

// ProjectUsage tracks per-project cost and message stats.
type ProjectUsage struct {
	TotalCostUSD   float64 `json:"total_cost_usd"`
	SessionCostUSD float64 `json:"session_cost_usd"`
	TotalMessages  int     `json:"total_messages"`
}

// NewProjectsFile creates a default ProjectsFile with a single "default" project.
func NewProjectsFile(workDir string, addDirs []string) ProjectsFile {
	return ProjectsFile{
		Active: "default",
		Projects: map[string]ProjectConfig{
			"default": {
				Name:      "default",
				WorkDir:   workDir,
				AddDirs:   addDirs,
				CreatedAt: time.Now().UTC().Format(time.RFC3339),
			},
		},
	}
}
