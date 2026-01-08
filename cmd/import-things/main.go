// import-things migrates data from Things 3 to the bud2 GTD system.
//
// Usage: go run ./cmd/import-things [--dry-run]
package main

import (
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// Things database constants
const (
	// Task types
	typeTask    = 0
	typeProject = 1
	typeHeading = 2

	// Status values
	statusOpen      = 0
	statusCanceled  = 2
	statusCompleted = 3

	// Start values
	startSomeday  = 0
	startAnytime  = 1
	startToday    = 2 // or scheduled
)

// Apple's reference date for date integers (2001-01-01)
var appleEpoch = time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)

// GTD types (matching internal/gtd/types.go)
type ChecklistItem struct {
	Text string `json:"text"`
	Done bool   `json:"done"`
}

type Task struct {
	ID          string          `json:"id"`
	Title       string          `json:"title"`
	Notes       string          `json:"notes,omitempty"`
	Checklist   []ChecklistItem `json:"checklist,omitempty"`
	When        string          `json:"when"`
	Project     string          `json:"project,omitempty"`
	Heading     string          `json:"heading,omitempty"`
	Area        string          `json:"area,omitempty"`
	Repeat      string          `json:"repeat,omitempty"`
	Status      string          `json:"status"`
	CompletedAt *time.Time      `json:"completed_at,omitempty"`
	Order       float64         `json:"order"`
}

type Project struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Notes    string   `json:"notes,omitempty"`
	When     string   `json:"when"`
	Area     string   `json:"area"`
	Headings []string `json:"headings"`
	Status   string   `json:"status"`
	Order    float64  `json:"order"`
}

type Area struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

type Store struct {
	Areas    []Area    `json:"areas"`
	Projects []Project `json:"projects"`
	Tasks    []Task    `json:"tasks"`
}

func main() {
	dryRun := flag.Bool("dry-run", false, "Print what would be imported without writing")
	flag.Parse()

	// Find Things database
	home, _ := os.UserHomeDir()
	thingsDir := filepath.Join(home, "Library/Group Containers/JLMPQHK86H.com.culturedcode.ThingsMac")

	// Find the ThingsData-* directory
	entries, err := os.ReadDir(thingsDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading Things directory: %v\n", err)
		os.Exit(1)
	}

	var dbPath string
	for _, entry := range entries {
		if entry.IsDir() && len(entry.Name()) > 10 && entry.Name()[:10] == "ThingsData" {
			dbPath = filepath.Join(thingsDir, entry.Name(), "Things Database.thingsdatabase/main.sqlite")
			break
		}
	}

	if dbPath == "" {
		fmt.Fprintf(os.Stderr, "Could not find Things database\n")
		os.Exit(1)
	}

	fmt.Printf("Reading from: %s\n", dbPath)

	// Open database
	db, err := sql.Open("sqlite3", dbPath+"?mode=ro")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error opening database: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	store := &Store{
		Areas:    []Area{},
		Projects: []Project{},
		Tasks:    []Task{},
	}

	// Import areas
	areas, err := importAreas(db)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error importing areas: %v\n", err)
		os.Exit(1)
	}
	store.Areas = areas
	fmt.Printf("Imported %d areas\n", len(areas))

	// Import projects and collect headings
	projects, headingMap, err := importProjects(db)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error importing projects: %v\n", err)
		os.Exit(1)
	}
	store.Projects = projects
	fmt.Printf("Imported %d projects\n", len(projects))

	// Import tasks
	tasks, err := importTasks(db, headingMap)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error importing tasks: %v\n", err)
		os.Exit(1)
	}
	store.Tasks = tasks
	fmt.Printf("Imported %d tasks\n", len(tasks))

	// Output
	if *dryRun {
		data, _ := json.MarshalIndent(store, "", "  ")
		fmt.Println(string(data))
	} else {
		outputPath := "state/user_tasks.json"
		data, _ := json.MarshalIndent(store, "", "  ")
		if err := os.WriteFile(outputPath, data, 0644); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing output: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Wrote to: %s\n", outputPath)
	}
}

func importAreas(db *sql.DB) ([]Area, error) {
	rows, err := db.Query(`SELECT uuid, title FROM TMArea ORDER BY "index"`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var areas []Area
	for rows.Next() {
		var uuid, title string
		if err := rows.Scan(&uuid, &title); err != nil {
			return nil, err
		}
		areas = append(areas, Area{
			ID:    uuid,
			Title: title,
		})
	}
	return areas, nil
}

func importProjects(db *sql.DB) ([]Project, map[string]string, error) {
	// First get all headings
	headingMap := make(map[string]string) // heading uuid -> heading title
	headingRows, err := db.Query(`SELECT uuid, title FROM TMTask WHERE type = ? AND trashed = 0`, typeHeading)
	if err != nil {
		return nil, nil, err
	}
	for headingRows.Next() {
		var uuid, title string
		if err := headingRows.Scan(&uuid, &title); err != nil {
			headingRows.Close()
			return nil, nil, err
		}
		headingMap[uuid] = title
	}
	headingRows.Close()

	// Get headings per project
	projectHeadings := make(map[string][]string) // project uuid -> ordered heading titles
	headingOrderRows, err := db.Query(`
		SELECT uuid, title, project
		FROM TMTask
		WHERE type = ? AND trashed = 0 AND project IS NOT NULL AND project != ''
		ORDER BY "index"
	`, typeHeading)
	if err != nil {
		return nil, nil, err
	}
	for headingOrderRows.Next() {
		var uuid, title string
		var project sql.NullString
		if err := headingOrderRows.Scan(&uuid, &title, &project); err != nil {
			headingOrderRows.Close()
			return nil, nil, err
		}
		if project.Valid {
			projectHeadings[project.String] = append(projectHeadings[project.String], title)
		}
	}
	headingOrderRows.Close()

	// Now get projects
	rows, err := db.Query(`
		SELECT uuid, title, COALESCE(notes, ''), status, start, startDate, COALESCE(area, ''), "index"
		FROM TMTask
		WHERE type = ? AND trashed = 0
		ORDER BY "index"
	`, typeProject)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	var projects []Project
	for rows.Next() {
		var uuid, title, notes, area string
		var status, start int
		var startDate sql.NullInt64
		var index float64
		if err := rows.Scan(&uuid, &title, &notes, &status, &start, &startDate, &area, &index); err != nil {
			return nil, nil, err
		}

		when := convertWhen(start, startDate)
		gtdStatus := convertStatus(status)

		projects = append(projects, Project{
			ID:       uuid,
			Title:    title,
			Notes:    notes,
			When:     when,
			Area:     area,
			Headings: projectHeadings[uuid],
			Status:   gtdStatus,
			Order:    index,
		})
	}
	return projects, headingMap, nil
}

func importTasks(db *sql.DB, headingMap map[string]string) ([]Task, error) {
	rows, err := db.Query(`
		SELECT uuid, title, COALESCE(notes, ''), status, start, startDate,
		       COALESCE(area, ''), COALESCE(project, ''), COALESCE(heading, ''),
		       stopDate, "index"
		FROM TMTask
		WHERE type = ? AND trashed = 0
		ORDER BY "index"
	`, typeTask)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []Task
	for rows.Next() {
		var uuid, title, notes, area, project, headingUUID string
		var status, start int
		var startDate sql.NullInt64
		var stopDate sql.NullFloat64
		var index float64
		if err := rows.Scan(&uuid, &title, &notes, &status, &start, &startDate, &area, &project, &headingUUID, &stopDate, &index); err != nil {
			return nil, err
		}

		when := convertWhen(start, startDate)
		gtdStatus := convertStatus(status)

		// Convert heading UUID to title
		headingTitle := ""
		if headingUUID != "" {
			headingTitle = headingMap[headingUUID]
		}

		// Get completed time (stopDate is Unix timestamp, not Apple epoch)
		var completedAt *time.Time
		if gtdStatus == "completed" && stopDate.Valid {
			t := time.Unix(int64(stopDate.Float64), 0)
			completedAt = &t
		}

		// Inbox detection: unorganized tasks (no project, no area, anytime)
		finalWhen := when
		if when == "anytime" && project == "" && area == "" && gtdStatus == "open" {
			finalWhen = "inbox"
		}

		task := Task{
			ID:          uuid,
			Title:       title,
			Notes:       notes,
			When:        finalWhen,
			Project:     project,
			Heading:     headingTitle,
			Area:        area,
			Status:      gtdStatus,
			CompletedAt: completedAt,
			Order:       index,
		}

		// Get checklist items
		checklist, err := getChecklist(db, uuid)
		if err != nil {
			return nil, err
		}
		task.Checklist = checklist

		tasks = append(tasks, task)
	}
	return tasks, nil
}

func getChecklist(db *sql.DB, taskUUID string) ([]ChecklistItem, error) {
	rows, err := db.Query(`
		SELECT title, status
		FROM TMChecklistItem
		WHERE task = ?
		ORDER BY "index"
	`, taskUUID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []ChecklistItem
	for rows.Next() {
		var title string
		var status int
		if err := rows.Scan(&title, &status); err != nil {
			return nil, err
		}
		items = append(items, ChecklistItem{
			Text: title,
			Done: status == statusCompleted,
		})
	}
	return items, nil
}

func convertWhen(start int, startDate sql.NullInt64) string {
	switch start {
	case startSomeday:
		return "someday"
	case startToday:
		if startDate.Valid && startDate.Int64 > 0 && startDate.Int64 < 50000 {
			// Convert Apple date integer (days since 2001-01-01) to YYYY-MM-DD
			// Sanity check: 50000 days = ~137 years, anything larger is invalid
			date := appleEpoch.AddDate(0, 0, int(startDate.Int64))
			today := time.Now().Truncate(24 * time.Hour)
			if date.Equal(today) || date.Before(today) {
				return "today"
			}
			return date.Format("2006-01-02")
		}
		return "today"
	default: // startAnytime or unknown
		return "anytime"
	}
}

func convertStatus(status int) string {
	switch status {
	case statusCompleted:
		return "completed"
	case statusCanceled:
		return "canceled"
	default:
		return "open"
	}
}
