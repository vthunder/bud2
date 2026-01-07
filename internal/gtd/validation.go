package gtd

import "fmt"

// ValidateTask validates a task against GTD rules
func (s *GTDStore) ValidateTask(task *Task) error {
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
		project := s.GetProject(task.Project)
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
