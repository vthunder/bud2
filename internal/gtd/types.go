package gtd

import "time"

// ChecklistItem represents a sub-task within a task
type ChecklistItem struct {
	Text string `json:"text"`
	Done bool   `json:"done"`
}

// Task represents a GTD task (owner's task, not Bud's commitment)
type Task struct {
	ID          string          `json:"id"`
	Title       string          `json:"title"`
	Notes       string          `json:"notes,omitempty"`
	Checklist   []ChecklistItem `json:"checklist,omitempty"`
	When        string          `json:"when"`                   // inbox, today, anytime, someday, or YYYY-MM-DD
	Project     string          `json:"project,omitempty"`      // project ID
	Heading     string          `json:"heading,omitempty"`      // heading name within project
	Area        string          `json:"area,omitempty"`         // area ID (only if not in project)
	Repeat      string          `json:"repeat,omitempty"`       // daily, weekly, monthly, etc.
	Status      string          `json:"status"`                 // open, completed, canceled
	CompletedAt *time.Time      `json:"completed_at,omitempty"`
	Order       float64         `json:"order"`
}

// Project represents a GTD project (multi-step outcome)
type Project struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Notes    string   `json:"notes,omitempty"`
	When     string   `json:"when"`     // anytime, someday, or YYYY-MM-DD
	Area     string   `json:"area"`     // area ID
	Headings []string `json:"headings"` // ordered list of heading names
	Status   string   `json:"status"`   // open, completed, canceled
	Order    float64  `json:"order"`
}

// Area represents a GTD area of responsibility
type Area struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// Store represents the complete GTD data
type Store struct {
	Areas    []Area    `json:"areas"`
	Projects []Project `json:"projects"`
	Tasks    []Task    `json:"tasks"`
}
