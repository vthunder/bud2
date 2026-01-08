package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"github.com/vthunder/bud2/internal/activity"
	"github.com/vthunder/bud2/internal/gtd"
	"github.com/vthunder/bud2/internal/integrations/notion"
	"github.com/vthunder/bud2/internal/mcp"
	"github.com/vthunder/bud2/internal/motivation"
	"github.com/vthunder/bud2/internal/reflex"
	"github.com/vthunder/bud2/internal/state"
	"github.com/vthunder/bud2/internal/types"
)

func main() {
	// Log to stderr so stdout is clean for JSON-RPC
	log.SetOutput(os.Stderr)
	log.SetPrefix("[bud-mcp] ")

	// Load .env file if present (don't error if missing)
	if err := godotenv.Load(); err == nil {
		log.Println("Loaded .env file")
	}

	log.Println("Starting bud2 MCP server...")

	// Get state path from environment
	statePath := os.Getenv("BUD_STATE_PATH")
	if statePath == "" {
		statePath = "state"
	}

	systemPath := filepath.Join(statePath, "system")
	queuesPath := filepath.Join(systemPath, "queues")
	os.MkdirAll(queuesPath, 0755)
	outboxPath := filepath.Join(queuesPath, "outbox.jsonl")
	log.Printf("Outbox path: %s", outboxPath)

	// Create MCP server
	server := mcp.NewServer()

	// Get default channel from environment
	defaultChannel := os.Getenv("DISCORD_CHANNEL_ID")

	// Register talk_to_user tool
	server.RegisterTool("talk_to_user", func(ctx any, args map[string]any) (string, error) {
		message, ok := args["message"].(string)
		if !ok {
			return "", fmt.Errorf("message is required")
		}

		// Use provided channel_id or fall back to default
		channelID, _ := args["channel_id"].(string)
		if channelID == "" {
			channelID = defaultChannel
		}
		if channelID == "" {
			return "", fmt.Errorf("channel_id required (none provided and DISCORD_CHANNEL_ID not set)")
		}

		log.Printf("talk_to_user: channel=%s message=%s", channelID, truncate(message, 50))

		// Write action to outbox
		action := map[string]any{
			"id":       fmt.Sprintf("action-%d", time.Now().UnixNano()),
			"effector": "discord",
			"type":     "send_message",
			"payload": map[string]any{
				"channel_id": channelID,
				"content":    message,
			},
			"status":     "pending",
			"created_at": time.Now().Format(time.RFC3339),
		}

		// Append to outbox file
		f, err := os.OpenFile(outboxPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return "", fmt.Errorf("failed to open outbox: %w", err)
		}
		defer f.Close()

		data, err := json.Marshal(action)
		if err != nil {
			return "", fmt.Errorf("failed to marshal action: %w", err)
		}

		if _, err := f.WriteString(string(data) + "\n"); err != nil {
			return "", fmt.Errorf("failed to write to outbox: %w", err)
		}

		log.Printf("Action queued: %s", action["id"])
		return "Message queued for sending to Discord", nil
	})

	tracesPath := filepath.Join(systemPath, "traces.json")

	// Register list_traces tool (for discovering trace IDs)
	server.RegisterTool("list_traces", func(ctx any, args map[string]any) (string, error) {
		traces, err := loadTraces(tracesPath)
		if err != nil {
			return "", fmt.Errorf("failed to load traces: %w", err)
		}

		// Build summary
		var result []map[string]any
		for _, t := range traces {
			result = append(result, map[string]any{
				"id":       t.ID,
				"content":  truncate(t.Content, 100),
				"is_core":  t.IsCore,
				"strength": t.Strength,
			})
		}

		data, _ := json.MarshalIndent(result, "", "  ")
		return string(data), nil
	})

	// Register mark_core tool
	server.RegisterTool("mark_core", func(ctx any, args map[string]any) (string, error) {
		traceID, ok := args["trace_id"].(string)
		if !ok {
			return "", fmt.Errorf("trace_id is required")
		}

		isCore := true // default to marking as core
		if val, ok := args["is_core"].(bool); ok {
			isCore = val
		}

		traces, err := loadTraces(tracesPath)
		if err != nil {
			return "", fmt.Errorf("failed to load traces: %w", err)
		}

		// Find and update the trace
		found := false
		for _, t := range traces {
			if t.ID == traceID {
				t.IsCore = isCore
				found = true
				break
			}
		}

		if !found {
			return "", fmt.Errorf("trace %s not found", traceID)
		}

		// Save back
		if err := saveTraces(tracesPath, traces); err != nil {
			return "", fmt.Errorf("failed to save traces: %w", err)
		}

		action := "marked as core"
		if !isCore {
			action = "unmarked as core"
		}
		log.Printf("Trace %s %s", traceID, action)
		return fmt.Sprintf("Trace %s %s. Changes will take effect on next bud restart.", traceID, action), nil
	})

	// Register save_thought tool (save a thought to memory via inbox)
	server.RegisterTool("save_thought", func(ctx any, args map[string]any) (string, error) {
		content, ok := args["content"].(string)
		if !ok || content == "" {
			return "", fmt.Errorf("content is required")
		}

		// Write thought to inbox as a special message type
		thought := map[string]any{
			"id":        fmt.Sprintf("thought-%d", time.Now().UnixNano()),
			"type":      "thought",
			"content":   content,
			"timestamp": time.Now().Format(time.RFC3339),
			"status":    "pending",
		}

		// Append to inbox file
		f, err := os.OpenFile(filepath.Join(queuesPath, "inbox.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return "", fmt.Errorf("failed to open inbox: %w", err)
		}
		defer f.Close()

		data, err := json.Marshal(thought)
		if err != nil {
			return "", fmt.Errorf("failed to marshal thought: %w", err)
		}

		if _, err := f.WriteString(string(data) + "\n"); err != nil {
			return "", fmt.Errorf("failed to write thought: %w", err)
		}

		log.Printf("Saved thought: %s", truncate(content, 50))
		return "Thought saved to memory. It will be consolidated with other memories over time.", nil
	})

	// Register signal_done tool (signal that Claude is done thinking)
	server.RegisterTool("signal_done", func(ctx any, args map[string]any) (string, error) {
		sessionID, _ := args["session_id"].(string)
		summary, _ := args["summary"].(string)

		signal := map[string]any{
			"type":       "session_done",
			"session_id": sessionID,
			"summary":    summary,
			"timestamp":  time.Now().Format(time.RFC3339),
		}

		// Write to signals file (picked up by main bud process)
		signalsPath := filepath.Join(queuesPath, "signals.jsonl")
		f, err := os.OpenFile(signalsPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return "", fmt.Errorf("failed to open signals file: %w", err)
		}
		defer f.Close()

		data, _ := json.Marshal(signal)
		if _, err := f.WriteString(string(data) + "\n"); err != nil {
			return "", fmt.Errorf("failed to write signal: %w", err)
		}

		log.Printf("Session done signal: session=%s summary=%s", sessionID, truncate(summary, 50))
		return "Done signal recorded. Ready for new prompts.", nil
	})

	// Initialize activity log for observability
	activityLog := activity.New(statePath)

	// Register journal_log tool (writes to activity.jsonl for unified logging)
	// Kept for backwards compatibility - all logging now goes to activity.jsonl
	server.RegisterTool("journal_log", func(ctx any, args map[string]any) (string, error) {
		summary, _ := args["summary"].(string)
		context, _ := args["context"].(string)
		reasoning, _ := args["reasoning"].(string)
		outcome, _ := args["outcome"].(string)

		if summary == "" {
			return "", fmt.Errorf("summary is required")
		}

		// All journal entries now logged as activity decisions
		err := activityLog.LogDecision(summary, reasoning, context, outcome)
		if err != nil {
			return "", fmt.Errorf("failed to log entry: %w", err)
		}

		log.Printf("Activity logged: %s", truncate(summary, 50))
		return "Logged to activity.", nil
	})

	// Register journal_recent tool (reads from activity.jsonl)
	server.RegisterTool("journal_recent", func(ctx any, args map[string]any) (string, error) {
		count := 20 // default
		if n, ok := args["count"].(float64); ok && n > 0 {
			count = int(n)
		}

		entries, err := activityLog.Recent(count)
		if err != nil {
			return "", fmt.Errorf("failed to get entries: %w", err)
		}

		if len(entries) == 0 {
			return "No activity entries yet.", nil
		}

		data, _ := json.MarshalIndent(entries, "", "  ")
		return string(data), nil
	})

	// Register journal_today tool (reads from activity.jsonl)
	server.RegisterTool("journal_today", func(ctx any, args map[string]any) (string, error) {
		entries, err := activityLog.Today()
		if err != nil {
			return "", fmt.Errorf("failed to get today's entries: %w", err)
		}

		if len(entries) == 0 {
			return "No activity entries today yet.", nil
		}

		data, _ := json.MarshalIndent(entries, "", "  ")
		return string(data), nil
	})

	// Register activity_recent tool (get recent activity entries)
	server.RegisterTool("activity_recent", func(ctx any, args map[string]any) (string, error) {
		count := 50 // default
		if n, ok := args["count"].(float64); ok && n > 0 {
			count = int(n)
		}

		entries, err := activityLog.Recent(count)
		if err != nil {
			return "", fmt.Errorf("failed to get activity entries: %w", err)
		}

		if len(entries) == 0 {
			return "No activity entries yet.", nil
		}

		data, _ := json.MarshalIndent(entries, "", "  ")
		return string(data), nil
	})

	// Register activity_today tool (get today's activity entries)
	server.RegisterTool("activity_today", func(ctx any, args map[string]any) (string, error) {
		entries, err := activityLog.Today()
		if err != nil {
			return "", fmt.Errorf("failed to get today's activity: %w", err)
		}

		if len(entries) == 0 {
			return "No activity entries today yet.", nil
		}

		data, _ := json.MarshalIndent(entries, "", "  ")
		return string(data), nil
	})

	// Register activity_search tool (search activity by text)
	server.RegisterTool("activity_search", func(ctx any, args map[string]any) (string, error) {
		query, _ := args["query"].(string)
		if query == "" {
			return "", fmt.Errorf("query is required")
		}

		limit := 100 // default
		if n, ok := args["limit"].(float64); ok && n > 0 {
			limit = int(n)
		}

		entries, err := activityLog.Search(query, limit)
		if err != nil {
			return "", fmt.Errorf("failed to search activity: %w", err)
		}

		if len(entries) == 0 {
			return fmt.Sprintf("No activity entries matching '%s'.", query), nil
		}

		data, _ := json.MarshalIndent(entries, "", "  ")
		return string(data), nil
	})

	// Register activity_by_type tool (filter activity by event type)
	server.RegisterTool("activity_by_type", func(ctx any, args map[string]any) (string, error) {
		typeStr, _ := args["type"].(string)
		if typeStr == "" {
			// Return list of valid types
			return "Valid types: input, reflex, reflex_pass, executive_wake, executive_done, action, decision, error", nil
		}

		limit := 50 // default
		if n, ok := args["limit"].(float64); ok && n > 0 {
			limit = int(n)
		}

		entries, err := activityLog.ByType(activity.Type(typeStr), limit)
		if err != nil {
			return "", fmt.Errorf("failed to filter activity: %w", err)
		}

		if len(entries) == 0 {
			return fmt.Sprintf("No activity entries of type '%s'.", typeStr), nil
		}

		data, _ := json.MarshalIndent(entries, "", "  ")
		return string(data), nil
	})

	// Initialize motivation stores
	taskStore := motivation.NewTaskStore(statePath)
	ideaStore := motivation.NewIdeaStore(statePath)
	taskStore.Load()
	ideaStore.Load()

	// Register add_bud_task tool
	server.RegisterTool("add_bud_task", func(ctx any, args map[string]any) (string, error) {
		task, ok := args["task"].(string)
		if !ok || task == "" {
			return "", fmt.Errorf("task description is required")
		}

		t := &motivation.Task{
			Task:    task,
			Context: "",
			Status:  "pending",
		}

		if context, ok := args["context"].(string); ok {
			t.Context = context
		}
		if priority, ok := args["priority"].(float64); ok {
			t.Priority = int(priority)
		} else {
			t.Priority = 2 // default medium priority
		}
		if dueStr, ok := args["due"].(string); ok && dueStr != "" {
			if due := parseDueTime(dueStr); due != nil {
				t.Due = due
			} else {
				log.Printf("Warning: could not parse due time: %s", dueStr)
			}
		}

		taskStore.Add(t)
		if err := taskStore.Save(); err != nil {
			return "", fmt.Errorf("failed to save task: %w", err)
		}

		dueInfo := ""
		if t.Due != nil {
			dueInfo = fmt.Sprintf(" (due: %s)", t.Due.Format(time.RFC3339))
		}
		log.Printf("Added task: %s%s", truncate(task, 50), dueInfo)
		return fmt.Sprintf("Task added: %s (ID: %s)%s", task, t.ID, dueInfo), nil
	})

	// Register list_bud_tasks tool
	server.RegisterTool("list_bud_tasks", func(ctx any, args map[string]any) (string, error) {
		tasks := taskStore.GetPending()
		if len(tasks) == 0 {
			return "No pending tasks.", nil
		}

		data, _ := json.MarshalIndent(tasks, "", "  ")
		return string(data), nil
	})

	// Register complete_bud_task tool
	server.RegisterTool("complete_bud_task", func(ctx any, args map[string]any) (string, error) {
		taskID, ok := args["task_id"].(string)
		if !ok || taskID == "" {
			return "", fmt.Errorf("task_id is required")
		}

		task := taskStore.Get(taskID)
		if task == nil {
			return "", fmt.Errorf("task not found: %s", taskID)
		}

		taskStore.Complete(taskID)
		if err := taskStore.Save(); err != nil {
			return "", fmt.Errorf("failed to save: %w", err)
		}

		log.Printf("Completed task: %s", taskID)
		return fmt.Sprintf("Task completed: %s", task.Task), nil
	})

	// Register add_idea tool
	server.RegisterTool("add_idea", func(ctx any, args map[string]any) (string, error) {
		idea, ok := args["idea"].(string)
		if !ok || idea == "" {
			return "", fmt.Errorf("idea description is required")
		}

		i := &motivation.Idea{
			Idea:     idea,
			Priority: 2, // default medium
		}

		if sparkedBy, ok := args["sparked_by"].(string); ok {
			i.SparkBy = sparkedBy
		}
		if priority, ok := args["priority"].(float64); ok {
			i.Priority = int(priority)
		}

		ideaStore.Add(i)
		if err := ideaStore.Save(); err != nil {
			return "", fmt.Errorf("failed to save idea: %w", err)
		}

		log.Printf("Added idea: %s", truncate(idea, 50))
		return fmt.Sprintf("Idea saved for later exploration: %s (ID: %s)", idea, i.ID), nil
	})

	// Register list_ideas tool
	server.RegisterTool("list_ideas", func(ctx any, args map[string]any) (string, error) {
		ideas := ideaStore.GetUnexplored()
		if len(ideas) == 0 {
			return "No unexplored ideas.", nil
		}

		data, _ := json.MarshalIndent(ideas, "", "  ")
		return string(data), nil
	})

	// Register explore_idea tool (mark as explored with notes)
	server.RegisterTool("explore_idea", func(ctx any, args map[string]any) (string, error) {
		ideaID, ok := args["idea_id"].(string)
		if !ok || ideaID == "" {
			return "", fmt.Errorf("idea_id is required")
		}

		idea := ideaStore.Get(ideaID)
		if idea == nil {
			return "", fmt.Errorf("idea not found: %s", ideaID)
		}

		notes := ""
		if n, ok := args["notes"].(string); ok {
			notes = n
		}

		ideaStore.MarkExplored(ideaID, notes)
		if err := ideaStore.Save(); err != nil {
			return "", fmt.Errorf("failed to save: %w", err)
		}

		log.Printf("Explored idea: %s", ideaID)
		return fmt.Sprintf("Idea explored: %s", idea.Idea), nil
	})

	// Initialize reflex engine
	reflexEngine := reflex.NewEngine(statePath)
	reflexEngine.Load()

	// Register create_reflex tool
	server.RegisterTool("create_reflex", func(ctx any, args map[string]any) (string, error) {
		name, ok := args["name"].(string)
		if !ok || name == "" {
			return "", fmt.Errorf("name is required")
		}

		pattern := ""
		if p, ok := args["pattern"].(string); ok {
			pattern = p
		}

		description := ""
		if d, ok := args["description"].(string); ok {
			description = d
		}

		// Parse pipeline from JSON array
		var pipeline reflex.Pipeline
		if pipelineJSON, ok := args["pipeline"].([]any); ok {
			for _, step := range pipelineJSON {
				if stepMap, ok := step.(map[string]any); ok {
					ps := reflex.PipelineStep{
						Params: make(map[string]any),
					}
					if a, ok := stepMap["action"].(string); ok {
						ps.Action = a
					}
					if i, ok := stepMap["input"].(string); ok {
						ps.Input = i
					}
					if o, ok := stepMap["output"].(string); ok {
						ps.Output = o
					}
					for k, v := range stepMap {
						if k != "action" && k != "input" && k != "output" {
							ps.Params[k] = v
						}
					}
					pipeline = append(pipeline, ps)
				}
			}
		}

		r := &reflex.Reflex{
			Name:        name,
			Description: description,
			Trigger: reflex.Trigger{
				Pattern: pattern,
			},
			Pipeline: pipeline,
		}

		// Parse extract if provided
		if extract, ok := args["extract"].([]any); ok {
			for _, e := range extract {
				if s, ok := e.(string); ok {
					r.Trigger.Extract = append(r.Trigger.Extract, s)
				}
			}
		}

		if err := reflexEngine.SaveReflex(r); err != nil {
			return "", fmt.Errorf("failed to save reflex: %w", err)
		}

		log.Printf("Created reflex: %s", name)
		return fmt.Sprintf("Reflex '%s' created and saved. It will be active on next message.", name), nil
	})

	// Register list_reflexes tool
	server.RegisterTool("list_reflexes", func(ctx any, args map[string]any) (string, error) {
		reflexes := reflexEngine.List()
		if len(reflexes) == 0 {
			return "No reflexes defined.", nil
		}

		var result []map[string]any
		for _, r := range reflexes {
			result = append(result, map[string]any{
				"name":        r.Name,
				"description": r.Description,
				"pattern":     r.Trigger.Pattern,
				"fire_count":  r.FireCount,
			})
		}

		data, _ := json.MarshalIndent(result, "", "  ")
		return string(data), nil
	})

	// Register delete_reflex tool
	server.RegisterTool("delete_reflex", func(ctx any, args map[string]any) (string, error) {
		name, ok := args["name"].(string)
		if !ok || name == "" {
			return "", fmt.Errorf("name is required")
		}

		if err := reflexEngine.Delete(name); err != nil {
			return "", err
		}

		return fmt.Sprintf("Reflex '%s' deleted.", name), nil
	})

	// Initialize GTD store (user's tasks, not Bud's commitments)
	gtdStore := gtd.NewGTDStore(statePath)
	if err := gtdStore.Load(); err != nil {
		log.Printf("Warning: Failed to load GTD store: %v", err)
	}

	// Register gtd_add tool
	server.RegisterTool("gtd_add", func(ctx any, args map[string]any) (string, error) {
		title, ok := args["title"].(string)
		if !ok || title == "" {
			return "", fmt.Errorf("title is required")
		}

		task := &gtd.Task{
			Title: title,
		}

		// Optional fields
		if notes, ok := args["notes"].(string); ok {
			task.Notes = notes
		}
		if when, ok := args["when"].(string); ok && when != "" {
			task.When = when
		}
		if project, ok := args["project"].(string); ok {
			task.Project = project
		}
		if heading, ok := args["heading"].(string); ok {
			task.Heading = heading
		}
		if area, ok := args["area"].(string); ok {
			task.Area = area
		}

		// Validate before adding
		if err := gtdStore.ValidateTask(task); err != nil {
			return "", fmt.Errorf("validation failed: %w", err)
		}

		gtdStore.AddTask(task)
		if err := gtdStore.Save(); err != nil {
			return "", fmt.Errorf("failed to save task: %w", err)
		}

		location := task.When
		if task.Project != "" {
			if p := gtdStore.GetProject(task.Project); p != nil {
				location = fmt.Sprintf("project '%s'", p.Title)
				if task.Heading != "" {
					location += fmt.Sprintf(" / %s", task.Heading)
				}
			}
		}

		log.Printf("Added GTD task: %s (when: %s)", truncate(title, 50), location)
		return fmt.Sprintf("Task added to %s: %s (ID: %s)", location, title, task.ID), nil
	})

	// Register gtd_list tool
	server.RegisterTool("gtd_list", func(ctx any, args map[string]any) (string, error) {
		when, _ := args["when"].(string)
		project, _ := args["project"].(string)
		area, _ := args["area"].(string)
		status, _ := args["status"].(string)
		if status == "" {
			status = "open" // default to open tasks
		}

		tasks := gtdStore.GetTasks(when, project, area)

		// Filter by status
		var filtered []gtd.Task
		for _, t := range tasks {
			if status == "all" || t.Status == status {
				filtered = append(filtered, t)
			}
		}

		if len(filtered) == 0 {
			filterDesc := ""
			if when != "" {
				filterDesc += fmt.Sprintf(" when=%s", when)
			}
			if project != "" {
				filterDesc += fmt.Sprintf(" project=%s", project)
			}
			if area != "" {
				filterDesc += fmt.Sprintf(" area=%s", area)
			}
			if status != "all" {
				filterDesc += fmt.Sprintf(" status=%s", status)
			}
			if filterDesc == "" {
				return "No tasks found.", nil
			}
			return fmt.Sprintf("No tasks found matching:%s", filterDesc), nil
		}

		data, _ := json.MarshalIndent(filtered, "", "  ")
		return string(data), nil
	})

	// Register gtd_update tool
	server.RegisterTool("gtd_update", func(ctx any, args map[string]any) (string, error) {
		id, ok := args["id"].(string)
		if !ok || id == "" {
			return "", fmt.Errorf("id is required")
		}

		// Get existing task
		task := gtdStore.GetTask(id)
		if task == nil {
			return "", fmt.Errorf("task not found: %s", id)
		}

		// Track what was updated for the response
		var updates []string

		// Update provided fields
		if title, ok := args["title"].(string); ok {
			task.Title = title
			updates = append(updates, "title")
		}
		if notes, ok := args["notes"].(string); ok {
			task.Notes = notes
			updates = append(updates, "notes")
		}
		if when, ok := args["when"].(string); ok {
			task.When = when
			updates = append(updates, "when")
		}
		if project, ok := args["project"].(string); ok {
			task.Project = project
			updates = append(updates, "project")
		}
		if heading, ok := args["heading"].(string); ok {
			task.Heading = heading
			updates = append(updates, "heading")
		}
		if area, ok := args["area"].(string); ok {
			task.Area = area
			updates = append(updates, "area")
		}
		if checklistRaw, ok := args["checklist"].([]any); ok {
			var checklist []gtd.ChecklistItem
			for _, item := range checklistRaw {
				if itemMap, ok := item.(map[string]any); ok {
					ci := gtd.ChecklistItem{}
					if text, ok := itemMap["text"].(string); ok {
						ci.Text = text
					}
					if done, ok := itemMap["done"].(bool); ok {
						ci.Done = done
					}
					checklist = append(checklist, ci)
				}
			}
			task.Checklist = checklist
			updates = append(updates, "checklist")
		}

		// Validate the updated task
		if err := gtdStore.ValidateTask(task); err != nil {
			return "", fmt.Errorf("validation failed: %w", err)
		}

		// Save the update
		if err := gtdStore.UpdateTask(task); err != nil {
			return "", fmt.Errorf("failed to update task: %w", err)
		}
		if err := gtdStore.Save(); err != nil {
			return "", fmt.Errorf("failed to save: %w", err)
		}

		log.Printf("Updated GTD task: %s (fields: %v)", id, updates)
		return fmt.Sprintf("Task updated: %s (updated: %s)", task.Title, strings.Join(updates, ", ")), nil
	})

	// Register gtd_complete tool
	server.RegisterTool("gtd_complete", func(ctx any, args map[string]any) (string, error) {
		id, ok := args["id"].(string)
		if !ok || id == "" {
			return "", fmt.Errorf("id is required")
		}

		// Get task first to include info in response
		task := gtdStore.GetTask(id)
		if task == nil {
			return "", fmt.Errorf("task not found: %s", id)
		}

		taskTitle := task.Title
		isRepeating := task.Repeat != ""

		// Complete the task (handles repeating tasks internally)
		if err := gtdStore.CompleteTask(id); err != nil {
			return "", fmt.Errorf("failed to complete task: %w", err)
		}
		if err := gtdStore.Save(); err != nil {
			return "", fmt.Errorf("failed to save: %w", err)
		}

		log.Printf("Completed GTD task: %s (repeating: %v)", id, isRepeating)
		if isRepeating {
			return fmt.Sprintf("Task completed: %s (next occurrence created for repeating task)", taskTitle), nil
		}
		return fmt.Sprintf("Task completed: %s", taskTitle), nil
	})

	// Register gtd_areas tool
	server.RegisterTool("gtd_areas", func(ctx any, args map[string]any) (string, error) {
		action, _ := args["action"].(string)
		if action == "" {
			return "", fmt.Errorf("action is required (list, add, or update)")
		}

		switch action {
		case "list":
			areas := gtdStore.GetAreas()
			if len(areas) == 0 {
				return "No areas defined.", nil
			}
			data, _ := json.MarshalIndent(areas, "", "  ")
			return string(data), nil

		case "add":
			title, ok := args["title"].(string)
			if !ok || title == "" {
				return "", fmt.Errorf("title is required for add")
			}

			area := &gtd.Area{
				Title: title,
			}
			gtdStore.AddArea(area)
			if err := gtdStore.Save(); err != nil {
				return "", fmt.Errorf("failed to save area: %w", err)
			}

			log.Printf("Added GTD area: %s", title)
			return fmt.Sprintf("Area added: %s (ID: %s)", title, area.ID), nil

		case "update":
			id, ok := args["id"].(string)
			if !ok || id == "" {
				return "", fmt.Errorf("id is required for update")
			}

			area := gtdStore.GetArea(id)
			if area == nil {
				return "", fmt.Errorf("area not found: %s", id)
			}

			// Update title if provided
			if title, ok := args["title"].(string); ok && title != "" {
				area.Title = title
			}

			if err := gtdStore.UpdateArea(area); err != nil {
				return "", fmt.Errorf("failed to update area: %w", err)
			}
			if err := gtdStore.Save(); err != nil {
				return "", fmt.Errorf("failed to save: %w", err)
			}

			log.Printf("Updated GTD area: %s", id)
			return fmt.Sprintf("Area updated: %s", area.Title), nil

		default:
			return "", fmt.Errorf("unknown action: %s (use list, add, or update)", action)
		}
	})

	// Register gtd_projects tool
	server.RegisterTool("gtd_projects", func(ctx any, args map[string]any) (string, error) {
		action, _ := args["action"].(string)
		if action == "" {
			return "", fmt.Errorf("action is required (list, add, or update)")
		}

		switch action {
		case "list":
			when, _ := args["when"].(string)
			area, _ := args["area"].(string)
			projects := gtdStore.GetProjects(when, area)
			if len(projects) == 0 {
				filterDesc := ""
				if when != "" {
					filterDesc += fmt.Sprintf(" when=%s", when)
				}
				if area != "" {
					filterDesc += fmt.Sprintf(" area=%s", area)
				}
				if filterDesc == "" {
					return "No projects found.", nil
				}
				return fmt.Sprintf("No projects found matching:%s", filterDesc), nil
			}
			data, _ := json.MarshalIndent(projects, "", "  ")
			return string(data), nil

		case "add":
			title, ok := args["title"].(string)
			if !ok || title == "" {
				return "", fmt.Errorf("title is required for add")
			}

			project := &gtd.Project{
				Title: title,
			}

			// Optional fields
			if notes, ok := args["notes"].(string); ok {
				project.Notes = notes
			}
			if when, ok := args["when"].(string); ok && when != "" {
				project.When = when
			}
			if area, ok := args["area"].(string); ok {
				project.Area = area
			}
			if headingsRaw, ok := args["headings"].([]any); ok {
				var headings []string
				for _, h := range headingsRaw {
					if hs, ok := h.(string); ok {
						headings = append(headings, hs)
					}
				}
				project.Headings = headings
			}

			// Validate project before adding
			if err := gtdStore.ValidateProject(project); err != nil {
				return "", fmt.Errorf("validation failed: %w", err)
			}

			gtdStore.AddProject(project)
			if err := gtdStore.Save(); err != nil {
				return "", fmt.Errorf("failed to save project: %w", err)
			}

			log.Printf("Added GTD project: %s", title)
			return fmt.Sprintf("Project added: %s (ID: %s)", title, project.ID), nil

		case "update":
			id, ok := args["id"].(string)
			if !ok || id == "" {
				return "", fmt.Errorf("id is required for update")
			}

			project := gtdStore.GetProject(id)
			if project == nil {
				return "", fmt.Errorf("project not found: %s", id)
			}

			// Track what was updated
			var updates []string

			// Update provided fields
			if title, ok := args["title"].(string); ok && title != "" {
				project.Title = title
				updates = append(updates, "title")
			}
			if notes, ok := args["notes"].(string); ok {
				project.Notes = notes
				updates = append(updates, "notes")
			}
			if when, ok := args["when"].(string); ok {
				project.When = when
				updates = append(updates, "when")
			}
			if area, ok := args["area"].(string); ok {
				project.Area = area
				updates = append(updates, "area")
			}
			if headingsRaw, ok := args["headings"].([]any); ok {
				var headings []string
				for _, h := range headingsRaw {
					if hs, ok := h.(string); ok {
						headings = append(headings, hs)
					}
				}
				project.Headings = headings
				updates = append(updates, "headings")
			}

			if err := gtdStore.UpdateProject(project); err != nil {
				return "", fmt.Errorf("failed to update project: %w", err)
			}
			if err := gtdStore.Save(); err != nil {
				return "", fmt.Errorf("failed to save: %w", err)
			}

			log.Printf("Updated GTD project: %s (fields: %v)", id, updates)
			return fmt.Sprintf("Project updated: %s (updated: %s)", project.Title, strings.Join(updates, ", ")), nil

		default:
			return "", fmt.Errorf("unknown action: %s (use list, add, or update)", action)
		}
	})

	// Register create_core tool (create a new core trace directly)
	server.RegisterTool("create_core", func(ctx any, args map[string]any) (string, error) {
		content, ok := args["content"].(string)
		if !ok || content == "" {
			return "", fmt.Errorf("content is required")
		}

		traces, err := loadTraces(tracesPath)
		if err != nil {
			// If file doesn't exist, start with empty list
			traces = []*types.Trace{}
		}

		// Create new core trace
		trace := &types.Trace{
			ID:         fmt.Sprintf("core-%d", time.Now().UnixNano()),
			Content:    content,
			Activation: 1.0,
			Strength:   100,
			IsCore:     true,
			CreatedAt:  time.Now(),
			LastAccess: time.Now(),
		}
		traces = append(traces, trace)

		// Save
		if err := saveTraces(tracesPath, traces); err != nil {
			return "", fmt.Errorf("failed to save traces: %w", err)
		}

		log.Printf("Created core trace: %s", truncate(content, 50))
		return fmt.Sprintf("Created core trace %s. Changes will take effect on next bud restart.", trace.ID), nil
	})

	// Notion tools (only register if NOTION_API_KEY is set)
	if os.Getenv("NOTION_API_KEY") != "" {
		notionClient, err := notion.NewClient()
		if err != nil {
			log.Printf("Warning: Failed to create Notion client: %v", err)
		} else {
			log.Println("Notion integration enabled")

			// Register notion_search tool
			server.RegisterTool("notion_search", func(ctx any, args map[string]any) (string, error) {
				query, _ := args["query"].(string)
				if query == "" {
					return "", fmt.Errorf("query is required")
				}

				params := notion.SearchParams{
					Query:    query,
					PageSize: 20,
				}

				// Optional filter by type
				if filterType, ok := args["filter"].(string); ok && filterType != "" {
					params.Filter = &notion.SearchFilter{
						Property: "object",
						Value:    filterType,
					}
				}

				result, err := notionClient.Search(params)
				if err != nil {
					return "", err
				}

				// Format results
				var output []map[string]string
				for _, obj := range result.Results {
					output = append(output, map[string]string{
						"id":    obj.ID,
						"type":  obj.Object,
						"title": obj.GetTitle(),
						"url":   obj.URL,
					})
				}

				data, _ := json.MarshalIndent(output, "", "  ")
				return string(data), nil
			})

			// Register notion_get_page tool
			server.RegisterTool("notion_get_page", func(ctx any, args map[string]any) (string, error) {
				pageID, _ := args["page_id"].(string)
				if pageID == "" {
					return "", fmt.Errorf("page_id is required")
				}

				page, err := notionClient.GetPage(pageID)
				if err != nil {
					return "", err
				}

				// Format page properties
				props := make(map[string]string)
				for name := range page.Properties {
					props[name] = page.GetPropertyText(name)
				}

				result := map[string]any{
					"id":         page.ID,
					"title":      page.GetTitle(),
					"url":        page.URL,
					"properties": props,
				}

				data, _ := json.MarshalIndent(result, "", "  ")
				return string(data), nil
			})

			// Register notion_get_database tool
			server.RegisterTool("notion_get_database", func(ctx any, args map[string]any) (string, error) {
				dbID, _ := args["database_id"].(string)
				if dbID == "" {
					return "", fmt.Errorf("database_id is required")
				}

				db, err := notionClient.GetDatabase(dbID)
				if err != nil {
					return "", err
				}

				// Format schema
				schema := make(map[string]any)
				for name, prop := range db.Properties {
					propInfo := map[string]any{
						"type": prop.Type,
					}
					// Include options for select/status types
					if prop.Select != nil {
						var options []string
						for _, opt := range prop.Select.Options {
							options = append(options, opt.Name)
						}
						propInfo["options"] = options
					}
					if prop.MultiSelect != nil {
						var options []string
						for _, opt := range prop.MultiSelect.Options {
							options = append(options, opt.Name)
						}
						propInfo["options"] = options
					}
					if prop.Status != nil {
						var options []string
						for _, opt := range prop.Status.Options {
							options = append(options, opt.Name)
						}
						propInfo["options"] = options
					}
					schema[name] = propInfo
				}

				// Get title
				var title string
				if len(db.Title) > 0 {
					title = db.Title[0].PlainText
				}

				result := map[string]any{
					"id":     db.ID,
					"title":  title,
					"url":    db.URL,
					"schema": schema,
				}

				data, _ := json.MarshalIndent(result, "", "  ")
				return string(data), nil
			})

			// Register notion_query_database tool
			server.RegisterTool("notion_query_database", func(ctx any, args map[string]any) (string, error) {
				dbID, _ := args["database_id"].(string)
				if dbID == "" {
					return "", fmt.Errorf("database_id is required")
				}

				params := notion.QueryParams{
					PageSize: 50,
				}

				// Parse optional filter JSON
				if filterStr, ok := args["filter"].(string); ok && filterStr != "" {
					var filter any
					if err := json.Unmarshal([]byte(filterStr), &filter); err != nil {
						return "", fmt.Errorf("invalid filter JSON: %w", err)
					}
					params.Filter = filter
				}

				// Optional sort
				if sortProp, ok := args["sort_property"].(string); ok && sortProp != "" {
					direction := "descending"
					if d, ok := args["sort_direction"].(string); ok && d != "" {
						direction = d
					}
					params.Sorts = []notion.Sort{{
						Property:  sortProp,
						Direction: direction,
					}}
				}

				result, err := notionClient.QueryDatabase(dbID, params)
				if err != nil {
					return "", err
				}

				// Format results
				var output []map[string]any
				for _, obj := range result.Results {
					props := make(map[string]string)
					for name := range obj.Properties {
						props[name] = obj.GetPropertyText(name)
					}
					output = append(output, map[string]any{
						"id":         obj.ID,
						"title":      obj.GetTitle(),
						"properties": props,
					})
				}

				data, _ := json.MarshalIndent(map[string]any{
					"results":  output,
					"has_more": result.HasMore,
					"count":    len(output),
				}, "", "  ")
				return string(data), nil
			})
		}
	} else {
		log.Println("Notion integration disabled (NOTION_API_KEY not set)")
	}

	// State introspection tools
	stateInspector := state.NewInspector(statePath)

	server.RegisterTool("state_summary", func(ctx any, args map[string]any) (string, error) {
		summary, err := stateInspector.Summary()
		if err != nil {
			return "", err
		}
		data, _ := json.MarshalIndent(summary, "", "  ")
		return string(data), nil
	})

	server.RegisterTool("state_health", func(ctx any, args map[string]any) (string, error) {
		health, err := stateInspector.Health()
		if err != nil {
			return "", err
		}
		data, _ := json.MarshalIndent(health, "", "  ")
		return string(data), nil
	})

	server.RegisterTool("state_traces", func(ctx any, args map[string]any) (string, error) {
		action, _ := args["action"].(string)
		if action == "" {
			action = "list"
		}

		switch action {
		case "list":
			traces, err := stateInspector.ListTraces()
			if err != nil {
				return "", err
			}
			data, _ := json.MarshalIndent(traces, "", "  ")
			return string(data), nil

		case "show":
			id, ok := args["id"].(string)
			if !ok {
				return "", fmt.Errorf("id required for show action")
			}
			trace, err := stateInspector.GetTrace(id)
			if err != nil {
				return "", err
			}
			data, _ := json.MarshalIndent(trace, "", "  ")
			return string(data), nil

		case "delete":
			id, ok := args["id"].(string)
			if !ok {
				return "", fmt.Errorf("id required for delete action")
			}
			if err := stateInspector.DeleteTrace(id); err != nil {
				return "", err
			}
			return fmt.Sprintf("Deleted trace: %s", id), nil

		case "clear":
			clearCore, _ := args["clear_core"].(bool)
			count, err := stateInspector.ClearTraces(clearCore)
			if err != nil {
				return "", err
			}
			if clearCore {
				return fmt.Sprintf("Cleared %d core traces", count), nil
			}
			return fmt.Sprintf("Cleared %d non-core traces", count), nil

		case "regen_core":
			seedPath := filepath.Join(statePath, "core_seed.md")
			count, err := stateInspector.RegenCore(seedPath)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("Regenerated %d core traces", count), nil

		default:
			return "", fmt.Errorf("unknown action: %s", action)
		}
	})

	server.RegisterTool("state_percepts", func(ctx any, args map[string]any) (string, error) {
		action, _ := args["action"].(string)
		if action == "" {
			action = "list"
		}

		switch action {
		case "list":
			percepts, err := stateInspector.ListPercepts()
			if err != nil {
				return "", err
			}
			data, _ := json.MarshalIndent(percepts, "", "  ")
			return string(data), nil

		case "count":
			percepts, err := stateInspector.ListPercepts()
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("%d", len(percepts)), nil

		case "clear":
			olderThan, _ := args["older_than"].(string)
			var dur time.Duration
			if olderThan != "" {
				var err error
				dur, err = time.ParseDuration(olderThan)
				if err != nil {
					return "", fmt.Errorf("invalid duration: %w", err)
				}
			}
			count, err := stateInspector.ClearPercepts(dur)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("Cleared %d percepts", count), nil

		default:
			return "", fmt.Errorf("unknown action: %s", action)
		}
	})

	server.RegisterTool("state_threads", func(ctx any, args map[string]any) (string, error) {
		action, _ := args["action"].(string)
		if action == "" {
			action = "list"
		}

		switch action {
		case "list":
			threads, err := stateInspector.ListThreads()
			if err != nil {
				return "", err
			}
			data, _ := json.MarshalIndent(threads, "", "  ")
			return string(data), nil

		case "show":
			id, ok := args["id"].(string)
			if !ok {
				return "", fmt.Errorf("id required for show action")
			}
			thread, err := stateInspector.GetThread(id)
			if err != nil {
				return "", err
			}
			data, _ := json.MarshalIndent(thread, "", "  ")
			return string(data), nil

		case "clear":
			statusStr, _ := args["status"].(string)
			var statusPtr *types.ThreadStatus
			if statusStr != "" {
				s := types.ThreadStatus(statusStr)
				statusPtr = &s
			}
			count, err := stateInspector.ClearThreads(statusPtr)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("Cleared %d threads", count), nil

		default:
			return "", fmt.Errorf("unknown action: %s", action)
		}
	})

	server.RegisterTool("state_logs", func(ctx any, args map[string]any) (string, error) {
		action, _ := args["action"].(string)
		if action == "" {
			action = "tail"
		}

		switch action {
		case "tail":
			count := 20
			if c, ok := args["count"].(float64); ok {
				count = int(c)
			}
			entries, err := stateInspector.TailLogs(count)
			if err != nil {
				return "", err
			}
			data, _ := json.MarshalIndent(entries, "", "  ")
			return string(data), nil

		case "truncate":
			keep := 100
			if k, ok := args["keep"].(float64); ok {
				keep = int(k)
			}
			if err := stateInspector.TruncateLogs(keep); err != nil {
				return "", err
			}
			return fmt.Sprintf("Truncated logs to %d entries", keep), nil

		default:
			return "", fmt.Errorf("unknown action: %s", action)
		}
	})

	server.RegisterTool("state_queues", func(ctx any, args map[string]any) (string, error) {
		action, _ := args["action"].(string)
		if action == "" {
			action = "list"
		}

		switch action {
		case "list":
			queues, err := stateInspector.ListQueues()
			if err != nil {
				return "", err
			}
			data, _ := json.MarshalIndent(queues, "", "  ")
			return string(data), nil

		case "clear":
			if err := stateInspector.ClearQueues(); err != nil {
				return "", err
			}
			return "Cleared all queues", nil

		default:
			return "", fmt.Errorf("unknown action: %s", action)
		}
	})

	server.RegisterTool("state_sessions", func(ctx any, args map[string]any) (string, error) {
		action, _ := args["action"].(string)
		if action == "" {
			action = "list"
		}

		switch action {
		case "list":
			sessions, err := stateInspector.ListSessions()
			if err != nil {
				return "", err
			}
			data, _ := json.MarshalIndent(sessions, "", "  ")
			return string(data), nil

		case "clear":
			if err := stateInspector.ClearSessions(); err != nil {
				return "", err
			}
			return "Cleared sessions", nil

		default:
			return "", fmt.Errorf("unknown action: %s", action)
		}
	})

	server.RegisterTool("state_regen_core", func(ctx any, args map[string]any) (string, error) {
		seedPath := filepath.Join(statePath, "core_seed.md")
		count, err := stateInspector.RegenCore(seedPath)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Regenerated %d core traces from %s", count, seedPath), nil
	})

	// Run server
	if err := server.Run(); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// loadTraces reads traces from the JSON file
func loadTraces(path string) ([]*types.Trace, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var file struct {
		Traces []*types.Trace `json:"traces"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, err
	}

	return file.Traces, nil
}

// saveTraces writes traces to the JSON file
func saveTraces(path string, traces []*types.Trace) error {
	file := struct {
		Traces []*types.Trace `json:"traces"`
	}{
		Traces: traces,
	}

	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0644)
}

// parseDueTime parses various due time formats
// Supports: RFC3339, relative times (2m, 1h, 30min, etc.), and common formats
func parseDueTime(s string) *time.Time {
	now := time.Now()

	// Try RFC3339 first
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return &t
	}

	// Try common date/time formats
	formats := []string{
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		"2006-01-02",
		"01/02/2006 15:04",
		"01/02/2006",
		"15:04",
	}
	for _, f := range formats {
		if t, err := time.ParseInLocation(f, s, now.Location()); err == nil {
			// For time-only format, use today's date
			if f == "15:04" {
				t = time.Date(now.Year(), now.Month(), now.Day(), t.Hour(), t.Minute(), 0, 0, now.Location())
				if t.Before(now) {
					t = t.Add(24 * time.Hour) // tomorrow if time already passed
				}
			}
			return &t
		}
	}

	// Try relative time parsing (e.g., "2m", "1h", "30min", "2 hours")
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimPrefix(s, "in ")

	// Parse duration-like strings
	var duration time.Duration
	var matched bool

	// Try standard Go duration first
	if d, err := time.ParseDuration(s); err == nil {
		duration = d
		matched = true
	}

	// Try custom patterns: "2 min", "1 hour", "30 minutes"
	if !matched {
		patterns := []struct {
			suffix string
			mult   time.Duration
		}{
			{"minutes", time.Minute},
			{"minute", time.Minute},
			{"mins", time.Minute},
			{"min", time.Minute},
			{"hours", time.Hour},
			{"hour", time.Hour},
			{"hrs", time.Hour},
			{"hr", time.Hour},
			{"days", 24 * time.Hour},
			{"day", 24 * time.Hour},
			{"seconds", time.Second},
			{"second", time.Second},
			{"secs", time.Second},
			{"sec", time.Second},
		}
		for _, p := range patterns {
			if strings.HasSuffix(s, p.suffix) {
				numStr := strings.TrimSpace(strings.TrimSuffix(s, p.suffix))
				if n, err := strconv.Atoi(numStr); err == nil {
					duration = time.Duration(n) * p.mult
					matched = true
					break
				}
			}
		}
	}

	if matched && duration > 0 {
		t := now.Add(duration)
		return &t
	}

	return nil
}
