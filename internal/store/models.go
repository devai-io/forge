package store

import (
	"encoding/json"
	"time"
)

// The JSON shapes below are docs/API.md, field for field. Nullable fields are
// pointers so they marshal as null rather than disappearing; slices are
// always non-nil so they marshal as [] rather than null.

type User struct {
	ID           int64     `json:"id"`
	Username     string    `json:"username"`
	Email        string    `json:"email"`
	DisplayName  string    `json:"display_name"`
	Timezone     string    `json:"timezone"`
	WeeklyGoal   int       `json:"weekly_goal"`
	Accent       string    `json:"accent"` // "" (default), a preset name or #rrggbb
	TOTPEnabled  bool      `json:"totp_enabled"`
	CheckupTime  string    `json:"checkup_time"`
	CheckupEmail bool      `json:"checkup_email"`
	CreatedAt    time.Time `json:"created_at"`

	PasswordHash    string `json:"-"`
	TOTPSecret      []byte `json:"-"` // sealed
	TOTPPending     []byte `json:"-"` // sealed
	TOTPLastCounter int64  `json:"-"`
}

type Session struct {
	ID         int64     `json:"id"`
	Current    bool      `json:"current"`
	UserAgent  string    `json:"user_agent"`
	IP         string    `json:"ip"`
	CreatedAt  time.Time `json:"created_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}

type Link struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

type ProjectStats struct {
	Total          int        `json:"total"`
	Backlog        int        `json:"backlog"`
	Todo           int        `json:"todo"`
	InProgress     int        `json:"in_progress"`
	Blocked        int        `json:"blocked"`
	Done           int        `json:"done"`
	Overdue        int        `json:"overdue"`
	Done7d         int        `json:"done_7d"`
	Created7d      int        `json:"created_7d"`
	Commits7d      int        `json:"commits_7d"`
	DirtyRepos     int        `json:"dirty_repos"`
	EndpointsTotal int        `json:"endpoints_total"`
	EndpointsUp    int        `json:"endpoints_up"`
	EndpointsDown  int        `json:"endpoints_down"`
	ActiveRuns     int        `json:"active_runs"`
	LastActivityAt *time.Time `json:"last_activity_at"`
	Progress       float64    `json:"progress"`
}

type Project struct {
	ID          int64        `json:"id"`
	Key         string       `json:"key"`
	Name        string       `json:"name"`
	Category    string       `json:"category"`
	Status      string       `json:"status"`
	Priority    int          `json:"priority"`
	Color       string       `json:"color"`
	Summary     string       `json:"summary"`
	Description string       `json:"description"`
	InfraNotes  string       `json:"infra_notes"`
	TargetDate  *string      `json:"target_date"`
	Links       []Link       `json:"links"`
	CreatedAt   time.Time    `json:"created_at"`
	UpdatedAt   time.Time    `json:"updated_at"`
	Stats       ProjectStats `json:"stats"`
}

type ProjectDetail struct {
	Project
	Repos     []Repo          `json:"repos"`
	Servers   []ProjectServer `json:"servers"`
	Endpoints []Endpoint      `json:"endpoints"`
}

type GitHead struct {
	Hash    string `json:"hash"`
	Subject string `json:"subject"`
	Author  string `json:"author"`
	At      string `json:"at"`
}

type GitStatus struct {
	Branch       string    `json:"branch"`
	Dirty        int       `json:"dirty"`
	Untracked    int       `json:"untracked"`
	Ahead        int       `json:"ahead"`
	Behind       int       `json:"behind"`
	Head         *GitHead  `json:"head"`
	Commits7d    int       `json:"commits_7d"`
	LastCommitAt *string   `json:"last_commit_at"`
	ScannedAt    string    `json:"scanned_at"`
	RunnerName   string    `json:"runner_name"`
	Error        string    `json:"error"`
	CI           *CIStatus `json:"ci"`
}

// CIStatus is the latest GitHub Actions run on the default branch, as the
// runner's `gh run list` saw it.
type CIStatus struct {
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	Workflow   string `json:"workflow"`
	Title      string `json:"title"`
	URL        string `json:"url"`
	SHA        string `json:"sha"`
	At         string `json:"at"`
}

type Repo struct {
	ID            int64      `json:"id"`
	ProjectID     int64      `json:"project_id"`
	Name          string     `json:"name"`
	Path          string     `json:"path"`
	RemoteURL     string     `json:"remote_url"`
	DefaultBranch string     `json:"default_branch"`
	Kind          string     `json:"kind"`
	Deploy        string     `json:"deploy"`
	Notes         string     `json:"notes"`
	SortOrder     int        `json:"sort_order"`
	Git           *GitStatus `json:"git"`
}

type ServerProject struct {
	Key   string `json:"key"`
	Name  string `json:"name"`
	Color string `json:"color"`
	Role  string `json:"role"`
}

type Server struct {
	ID            int64           `json:"id"`
	Name          string          `json:"name"`
	Role          string          `json:"role"`
	Provider      string          `json:"provider"`
	Arch          string          `json:"arch"`
	PublicAddress string          `json:"public_address"`
	TailscaleIP   string          `json:"tailscale_ip"`
	Environment   string          `json:"environment"`
	Critical      bool            `json:"critical"`
	Tags          []string        `json:"tags"`
	Notes         string          `json:"notes"`
	Projects      []ServerProject `json:"projects"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

type ProjectServer struct {
	ServerID    int64  `json:"server_id"`
	Name        string `json:"name"`
	Role        string `json:"role"`
	Environment string `json:"environment"`
	Critical    bool   `json:"critical"`
}

type Endpoint struct {
	ID            int64      `json:"id"`
	ProjectID     int64      `json:"project_id"`
	ProjectKey    string     `json:"project_key"`
	ProjectName   string     `json:"project_name"`
	ProjectColor  string     `json:"project_color"`
	Name          string     `json:"name"`
	URL           string     `json:"url"`
	Kind          string     `json:"kind"`
	ExpectStatus  int        `json:"expect_status"`
	Enabled       bool       `json:"enabled"`
	LastStatus    string     `json:"last_status"`
	LastCode      *int       `json:"last_code"`
	LastLatencyMS *int       `json:"last_latency_ms"`
	LastError     string     `json:"last_error"`
	LastCheckedAt *time.Time `json:"last_checked_at"`
	LastChangeAt  *time.Time `json:"last_change_at"`
	Uptime24h     *float64   `json:"uptime_24h"`
}

type EndpointCheck struct {
	At        time.Time `json:"at"`
	OK        bool      `json:"ok"`
	Code      *int      `json:"code"`
	LatencyMS *int      `json:"latency_ms"`
	Error     string    `json:"error"`
}

type Task struct {
	ID           int64      `json:"id"`
	ProjectID    int64      `json:"project_id"`
	ProjectKey   string     `json:"project_key"`
	ProjectName  string     `json:"project_name"`
	ProjectColor string     `json:"project_color"`
	Number       int        `json:"number"`
	Ref          string     `json:"ref"`
	Title        string     `json:"title"`
	Description  string     `json:"description"`
	Status       string     `json:"status"`
	Priority     string     `json:"priority"`
	Type         string     `json:"type"`
	Labels       []string   `json:"labels"`
	DueDate      *string    `json:"due_date"`
	Focus        bool       `json:"focus"`
	RepoID       *int64     `json:"repo_id"`
	RepoName     *string    `json:"repo_name"`
	Estimate     *int       `json:"estimate"`
	SortOrder    float64    `json:"sort_order"`
	CompletedAt  *time.Time `json:"completed_at"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	CommentCount int        `json:"comment_count"`
}

type Comment struct {
	ID        int64     `json:"id"`
	TaskID    int64     `json:"task_id"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}

type TaskDetail struct {
	Task
	Comments []Comment `json:"comments"`
	Runs     []Run     `json:"runs"`
}

type Activity struct {
	ID           int64     `json:"id"`
	ProjectID    *int64    `json:"project_id"`
	ProjectKey   *string   `json:"project_key"`
	ProjectColor *string   `json:"project_color"`
	TaskID       *int64    `json:"task_id"`
	TaskRef      *string   `json:"task_ref"`
	RunID        *int64    `json:"run_id"`
	Kind         string    `json:"kind"`
	Summary      string    `json:"summary"`
	CreatedAt    time.Time `json:"created_at"`
}

type RunnerCapabilities struct {
	Claude          bool            `json:"claude"`
	PermissionModes []string        `json:"permission_modes"`
	Commands        []string        `json:"commands"`
	CommandDetails  []CommandDetail `json:"command_details"`
	MaxConcurrent   int             `json:"max_concurrent"`
	CI              bool            `json:"ci"`
	Terminal        bool            `json:"terminal"`
	Code            bool            `json:"code"`
	// CodeGateway is where forge-api reaches the runner's VS Code gateway
	// (a tailnet address); not shown in the UI.
	CodeGateway string `json:"code_gateway,omitempty"`
}

type CommandDetail struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Repos       []string `json:"repos"`
	Confirm     bool     `json:"confirm"`
}

// Command finds a command's details; ok is false if the runner has no such
// command. Runners from before command_details existed report names only.
func (c RunnerCapabilities) Command(name string) (CommandDetail, bool) {
	for _, d := range c.CommandDetails {
		if d.Name == name {
			return d, true
		}
	}
	if OneOf(name, c.Commands) {
		return CommandDetail{Name: name, Repos: []string{}}, true
	}
	return CommandDetail{}, false
}

type Runner struct {
	ID           int64              `json:"id"`
	Name         string             `json:"name"`
	Role         string             `json:"role"`
	Hostname     string             `json:"hostname"`
	OS           string             `json:"os"`
	Version      string             `json:"version"`
	Online       bool               `json:"online"`
	LastSeenAt   *time.Time         `json:"last_seen_at"`
	Capabilities RunnerCapabilities `json:"capabilities"`
	Running      int                `json:"running"`
	CreatedAt    time.Time          `json:"created_at"`
	// PairExpiresAt is set while a pairing code is outstanding.
	PairExpiresAt *time.Time `json:"pair_expires_at"`
}

type Run struct {
	ID              int64      `json:"id"`
	RunnerID        int64      `json:"runner_id"`
	RunnerName      string     `json:"runner_name"`
	ProjectID       int64      `json:"project_id"`
	ProjectKey      string     `json:"project_key"`
	ProjectColor    string     `json:"project_color"`
	RepoID          int64      `json:"repo_id"`
	RepoName        string     `json:"repo_name"`
	TaskID          *int64     `json:"task_id"`
	TaskRef         *string    `json:"task_ref"`
	Kind            string     `json:"kind"`
	Prompt          string     `json:"prompt"`
	Command         string     `json:"command"`
	PermissionMode  string     `json:"permission_mode"`
	Model           string     `json:"model"`
	Worktree        bool       `json:"worktree"`
	ResumeRunID     *int64     `json:"resume_run_id"`
	Status          string     `json:"status"`
	CancelRequested bool       `json:"cancel_requested"`
	SessionID       string     `json:"session_id"`
	Result          string     `json:"result"`
	Error           string     `json:"error"`
	ExitCode        *int       `json:"exit_code"`
	CostUSD         *float64   `json:"cost_usd"`
	NumTurns        *int       `json:"num_turns"`
	DurationMS      *int64     `json:"duration_ms"`
	CreatedAt       time.Time  `json:"created_at"`
	StartedAt       *time.Time `json:"started_at"`
	FinishedAt      *time.Time `json:"finished_at"`

	// Only the runner needs these: where to run and which session to resume.
	RepoPath      string `json:"repo_path,omitempty"`
	ResumeSession string `json:"resume_session,omitempty"`
}

type RunEvent struct {
	Seq  int             `json:"seq"`
	At   time.Time       `json:"at"`
	Kind string          `json:"kind"`
	Data json.RawMessage `json:"data"`
}

type DayCount struct {
	Date    string `json:"date"`
	Done    int    `json:"done"`
	Created int    `json:"created"`
}

type DashboardStats struct {
	StreakDays   int `json:"streak_days"`
	BestStreak   int `json:"best_streak"`
	DoneToday    int `json:"done_today"`
	DoneWeek     int `json:"done_week"`
	DonePrevWeek int `json:"done_prev_week"`
	WeeklyGoal   int `json:"weekly_goal"`
	OpenTasks    int `json:"open_tasks"`
	InProgress   int `json:"in_progress"`
	Blocked      int `json:"blocked"`
	Overdue      int `json:"overdue"`
	DueSoon      int `json:"due_soon"`
}

type Dashboard struct {
	Today         string          `json:"today"`
	Stats         DashboardStats  `json:"stats"`
	Daily         []DayCount      `json:"daily"`
	Focus         []Task          `json:"focus"`
	Overdue       []Task          `json:"overdue"`
	Projects      []Project       `json:"projects"`
	EndpointsDown []Endpoint      `json:"endpoints_down"`
	Runners       []Runner        `json:"runners"`
	ActiveRuns    []Run           `json:"active_runs"`
	RecentRuns    []Run           `json:"recent_runs"`
	Activity      []Activity      `json:"activity"`
	Checkup       *CheckupSummary `json:"checkup"`
}

// Enumerations, shared by validation and the seed loader.
var (
	ProjectStatuses = []string{"live", "building", "radar", "paused", "archived"}
	Categories      = []string{"work", "personal"}
	RepoKinds       = []string{"api", "ui", "mobile", "infra", "lib", "site", "other"}
	Environments    = []string{"production", "staging", "dev", "infra"}
	EndpointKinds   = []string{"web", "api", "health"}
	TaskStatuses    = []string{"backlog", "todo", "in_progress", "blocked", "done"}
	Priorities      = []string{"urgent", "high", "medium", "low"}
	TaskTypes       = []string{"feature", "bug", "chore", "research", "ops"}
	RunStatuses     = []string{"queued", "running", "succeeded", "failed", "cancelled"}
	PermissionModes = []string{"plan", "acceptEdits", "auto", "dontAsk", "bypassPermissions"}
	RunnerRoles     = []string{"master", "ios", "worker"}
)

func OneOf(v string, allowed []string) bool {
	for _, a := range allowed {
		if v == a {
			return true
		}
	}
	return false
}
