package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/joho/godotenv"
	"github.com/vthunder/bud2/internal/integrations/notion"
	"github.com/vthunder/bud2/internal/journal"
	"github.com/vthunder/bud2/internal/mcp"
	"github.com/vthunder/bud2/internal/motivation"
	"github.com/vthunder/bud2/internal/reflex"
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

	outboxPath := filepath.Join(statePath, "outbox.jsonl")
	log.Printf("Outbox path: %s", outboxPath)

	// Create MCP server
	server := mcp.NewServer()

	// Register talk_to_user tool
	server.RegisterTool("talk_to_user", func(ctx any, args map[string]any) (string, error) {
		message, ok := args["message"].(string)
		if !ok {
			return "", fmt.Errorf("message is required")
		}

		channelID, ok := args["channel_id"].(string)
		if !ok {
			return "", fmt.Errorf("channel_id is required")
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

	tracesPath := filepath.Join(statePath, "traces.json")

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
		f, err := os.OpenFile(filepath.Join(statePath, "inbox.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
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
		signalsPath := filepath.Join(statePath, "signals.jsonl")
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

	// Initialize journal
	j := journal.New(statePath)

	// Register journal_log tool (log a decision or action for observability)
	server.RegisterTool("journal_log", func(ctx any, args map[string]any) (string, error) {
		entryType, _ := args["type"].(string)
		summary, _ := args["summary"].(string)
		context, _ := args["context"].(string)
		reasoning, _ := args["reasoning"].(string)
		outcome, _ := args["outcome"].(string)

		if summary == "" {
			return "", fmt.Errorf("summary is required")
		}

		// Map type string to EntryType
		var t journal.EntryType
		switch entryType {
		case "decision":
			t = journal.EntryDecision
		case "impulse":
			t = journal.EntryImpulse
		case "reflex":
			t = journal.EntryReflex
		case "exploration":
			t = journal.EntryExploration
		case "action":
			t = journal.EntryAction
		case "observation":
			t = journal.EntryObservation
		default:
			t = journal.EntryAction
		}

		entry := journal.Entry{
			Type:      t,
			Summary:   summary,
			Context:   context,
			Reasoning: reasoning,
			Outcome:   outcome,
		}

		if err := j.Log(entry); err != nil {
			return "", fmt.Errorf("failed to log journal entry: %w", err)
		}

		log.Printf("Journal entry: [%s] %s", entryType, truncate(summary, 50))
		return "Logged to journal.", nil
	})

	// Register journal_recent tool (get recent journal entries for observability)
	server.RegisterTool("journal_recent", func(ctx any, args map[string]any) (string, error) {
		count := 20 // default
		if n, ok := args["count"].(float64); ok && n > 0 {
			count = int(n)
		}

		entries, err := j.Recent(count)
		if err != nil {
			return "", fmt.Errorf("failed to get journal entries: %w", err)
		}

		if len(entries) == 0 {
			return "No journal entries yet.", nil
		}

		data, _ := json.MarshalIndent(entries, "", "  ")
		return string(data), nil
	})

	// Register journal_today tool (get today's journal entries)
	server.RegisterTool("journal_today", func(ctx any, args map[string]any) (string, error) {
		entries, err := j.Today()
		if err != nil {
			return "", fmt.Errorf("failed to get today's entries: %w", err)
		}

		if len(entries) == 0 {
			return "No journal entries today yet.", nil
		}

		data, _ := json.MarshalIndent(entries, "", "  ")
		return string(data), nil
	})

	// Initialize motivation stores
	taskStore := motivation.NewTaskStore(statePath)
	ideaStore := motivation.NewIdeaStore(statePath)
	taskStore.Load()
	ideaStore.Load()

	// Register add_task tool
	server.RegisterTool("add_task", func(ctx any, args map[string]any) (string, error) {
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
			if due, err := time.Parse(time.RFC3339, dueStr); err == nil {
				t.Due = due
			}
		}

		taskStore.Add(t)
		if err := taskStore.Save(); err != nil {
			return "", fmt.Errorf("failed to save task: %w", err)
		}

		log.Printf("Added task: %s", truncate(task, 50))
		return fmt.Sprintf("Task added: %s (ID: %s)", task, t.ID), nil
	})

	// Register list_tasks tool
	server.RegisterTool("list_tasks", func(ctx any, args map[string]any) (string, error) {
		tasks := taskStore.GetPending()
		if len(tasks) == 0 {
			return "No pending tasks.", nil
		}

		data, _ := json.MarshalIndent(tasks, "", "  ")
		return string(data), nil
	})

	// Register complete_task tool
	server.RegisterTool("complete_task", func(ctx any, args map[string]any) (string, error) {
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
