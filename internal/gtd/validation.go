package gtd

import (
	"fmt"
	"regexp"
)

// validWhenValues contains the allowed static values for the When field
var validWhenValues = map[string]bool{
	"inbox":   true,
	"today":   true,
	"anytime": true,
	"someday": true,
}

// datePattern matches YYYY-MM-DD format
var datePattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// isValidWhen checks if a when value is valid (static value or date pattern)
func isValidWhen(when string) bool {
	if when == "" {
		return true // empty defaults to "inbox" in AddTask
	}
	if validWhenValues[when] {
		return true
	}
	return datePattern.MatchString(when)
}

// ValidateTask validates a task against GTD rules
func (s *GTDStore) ValidateTask(task *Task) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Validate when field
	if !isValidWhen(task.When) {
		return fmt.Errorf("invalid when value '%s': must be inbox, today, anytime, someday, or a date (YYYY-MM-DD)", task.When)
	}

	// Inbox tasks cannot have project, area, or heading
	if task.When == "inbox" {
		if task.Project != "" {
			return fmt.Errorf("inbox tasks cannot be in a project")
		}
		if task.Area != "" {
			return fmt.Errorf("inbox tasks cannot be in an area")
		}
		if task.Heading != "" {
			return fmt.Errorf("inbox tasks cannot have a heading")
		}
	}

	// Task with heading must have a project
	if task.Heading != "" && task.Project == "" {
		return fmt.Errorf("task with heading must be in a project")
	}

	// If project is set, verify it exists and heading is valid
	if task.Project != "" {
		var project *Project
		for i := range s.data.Projects {
			if s.data.Projects[i].ID == task.Project {
				project = &s.data.Projects[i]
				break
			}
		}
		if project == nil {
			return fmt.Errorf("project not found: %s", task.Project)
		}

		// Verify heading exists in project
		if task.Heading != "" {
			found := false
			for _, h := range project.Headings {
				if h == task.Heading {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("heading '%s' not found in project '%s'", task.Heading, project.Title)
			}
		}
	}

	// If area is set, verify it exists
	if task.Area != "" {
		found := false
		for _, a := range s.data.Areas {
			if a.ID == task.Area {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("area not found: %s", task.Area)
		}
	}

	return nil
}

// ValidateProject validates a project
func (s *GTDStore) ValidateProject(project *Project) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Project must have an area
	if project.Area == "" {
		return fmt.Errorf("project must have an area")
	}

	// Verify area exists
	found := false
	for _, a := range s.data.Areas {
		if a.ID == project.Area {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("area not found: %s", project.Area)
	}

	return nil
}
