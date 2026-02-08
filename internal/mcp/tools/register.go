package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/vthunder/bud2/internal/activity"
	"github.com/vthunder/bud2/internal/graph"
	"github.com/vthunder/bud2/internal/gtd"
	"github.com/vthunder/bud2/internal/integrations/calendar"
	"github.com/vthunder/bud2/internal/integrations/github"
	"github.com/vthunder/bud2/internal/mcp"
	"github.com/vthunder/bud2/internal/motivation"
	"github.com/vthunder/bud2/internal/reflex"
	"github.com/vthunder/bud2/internal/types"
)

// RegisterAll registers all MCP tools with the given server and dependencies.
func RegisterAll(server *mcp.Server, deps *Dependencies) {
	registerCommunicationTools(server, deps)
	registerMemoryTools(server, deps)
	registerActivityTools(server, deps)
	registerMotivationTools(server, deps)
	registerStateTools(server, deps)

	if deps.GTDStore != nil {
		registerGTDTools(server, deps)
	}
	if deps.ReflexEngine != nil {
		registerReflexTools(server, deps)
	}
	if deps.CalendarClient != nil {
		registerCalendarTools(server, deps)
	}
	if deps.GitHubClient != nil {
		registerGitHubTools(server, deps)
	}
	if deps.MemoryJudge != nil {
		registerEvalTools(server, deps)
	}
}

func registerCommunicationTools(server *mcp.Server, deps *Dependencies) {
	// talk_to_user - send message to Discord
	server.RegisterTool("talk_to_user", mcp.ToolDef{
		Description: "Send a message to the user via Discord. Use this to respond to questions, share observations, ask clarifying questions, or give status updates.",
		Properties: map[string]mcp.PropDef{
			"message":    {Type: "string", Description: "The message to send to the user"},
			"channel_id": {Type: "string", Description: "The Discord channel ID to send to. Optional - if not provided, uses the default channel from DISCORD_CHANNEL_ID."},
		},
		Required: []string{"message"},
	}, func(ctx any, args map[string]any) (string, error) {
		message, ok := args["message"].(string)
		if !ok {
			return "", fmt.Errorf("message is required")
		}

		channelID, _ := args["channel_id"].(string)
		if channelID == "" {
			channelID = deps.DefaultChannel
		}
		if channelID == "" {
			return "", fmt.Errorf("channel_id required (none provided and DISCORD_CHANNEL_ID not set)")
		}

		log.Printf("talk_to_user: channel=%s message=%s", channelID, truncate(message, 50))

		// Notify that this MCP tool was called (for user response detection)
		if deps.OnMCPToolCall != nil {
			deps.OnMCPToolCall("talk_to_user")
		}

		// Direct effector required
		if deps.SendMessage == nil {
			return "", fmt.Errorf("SendMessage callback not configured")
		}

		if err := deps.SendMessage(channelID, message); err != nil {
			return "", fmt.Errorf("failed to send message: %w", err)
		}

		return "Message sent to Discord", nil
	})

	// discord_react - add emoji reaction to a Discord message
	server.RegisterTool("discord_react", mcp.ToolDef{
		Description: "Add an emoji reaction to a Discord message. Use this for lightweight acknowledgments like 👍 or ✅ instead of sending text messages.",
		Properties: map[string]mcp.PropDef{
			"message_id": {Type: "string", Description: "The Discord message ID to react to (format: discord-{channelID}-{messageID})"},
			"emoji":      {Type: "string", Description: "The emoji to react with (Unicode emoji like 👍 or custom format like :name:)"},
		},
		Required: []string{"message_id", "emoji"},
	}, func(ctx any, args map[string]any) (string, error) {
		messageID, ok := args["message_id"].(string)
		if !ok || messageID == "" {
			return "", fmt.Errorf("message_id is required")
		}

		emoji, ok := args["emoji"].(string)
		if !ok || emoji == "" {
			return "", fmt.Errorf("emoji is required")
		}

		// Parse message ID format: discord-{channelID}-{messageID}
		parts := strings.Split(messageID, "-")
		if len(parts) != 3 || parts[0] != "discord" {
			return "", fmt.Errorf("invalid message_id format (expected discord-{channelID}-{messageID})")
		}
		channelID := parts[1]
		discordMsgID := parts[2]

		log.Printf("discord_react: channel=%s message=%s emoji=%s", channelID, discordMsgID, emoji)

		// Notify that this MCP tool was called (for user response detection)
		if deps.OnMCPToolCall != nil {
			deps.OnMCPToolCall("discord_react")
		}

		// Direct effector required
		if deps.AddReaction == nil {
			return "", fmt.Errorf("AddReaction callback not configured")
		}

		if err := deps.AddReaction(channelID, discordMsgID, emoji); err != nil {
			return "", fmt.Errorf("failed to add reaction: %w", err)
		}

		return fmt.Sprintf("Reaction %s added to message", emoji), nil
	})

	// signal_done
	server.RegisterTool("signal_done", mcp.ToolDef{
		Description: "Signal that you have finished processing and are ready for new prompts. IMPORTANT: Always call this when you have completed responding to a message or finishing a task. This helps track thinking time and enables autonomous work scheduling.",
		Properties: map[string]mcp.PropDef{
			"session_id":  {Type: "string", Description: "The current session ID (if known)"},
			"summary":     {Type: "string", Description: "Brief summary of what was accomplished (optional)"},
			"memory_eval": {Type: "object", Description: "Memory usefulness ratings as {\"M1\": 5, \"M2\": 1} where 1=not useful, 5=very useful"},
		},
	}, func(ctx any, args map[string]any) (string, error) {
		sessionID, _ := args["session_id"].(string)
		summary, _ := args["summary"].(string)
		memoryEval, _ := args["memory_eval"].(map[string]any)

		extra := map[string]any{
			"session_id": sessionID,
		}
		if len(memoryEval) > 0 {
			extra["memory_eval"] = memoryEval
		}

		msg := map[string]any{
			"id":        fmt.Sprintf("signal-%d", time.Now().UnixNano()),
			"type":      "signal",
			"subtype":   "done",
			"content":   summary,
			"timestamp": time.Now().Format(time.RFC3339),
			"status":    "pending",
			"extra":     extra,
		}

		inboxPath := filepath.Join(deps.QueuesPath, "inbox.jsonl")
		f, err := os.OpenFile(inboxPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return "", fmt.Errorf("failed to open inbox file: %w", err)
		}
		defer f.Close()

		data, _ := json.Marshal(msg)
		if _, err := f.WriteString(string(data) + "\n"); err != nil {
			return "", fmt.Errorf("failed to write signal: %w", err)
		}

		if len(memoryEval) > 0 {
			log.Printf("Session done signal: session=%s summary=%s memory_eval=%v", sessionID, truncate(summary, 50), memoryEval)
		} else {
			log.Printf("Session done signal: session=%s summary=%s", sessionID, truncate(summary, 50))
		}
		return "Done signal recorded. Ready for new prompts.", nil
	})

	// save_thought - adds to in-memory inbox (gets stored to memory graph via normal processing)
	server.RegisterTool("save_thought", mcp.ToolDef{
		Description: "Save a thought or observation to memory. Use this to remember decisions, observations, or anything worth recalling later. These get consolidated with other memories over time.",
		Properties: map[string]mcp.PropDef{
			"content": {Type: "string", Description: "The thought or observation to save (e.g., 'User prefers morning check-ins')"},
		},
		Required: []string{"content"},
	}, func(ctx any, args map[string]any) (string, error) {
		content, ok := args["content"].(string)
		if !ok || content == "" {
			return "", fmt.Errorf("content is required")
		}

		if deps.AddThought == nil {
			return "", fmt.Errorf("AddThought callback not configured")
		}

		if err := deps.AddThought(content); err != nil {
			return "", fmt.Errorf("failed to save thought: %w", err)
		}

		log.Printf("Saved thought to inbox: %s", truncate(content, 50))
		return "Thought saved to memory. It will be consolidated with other memories over time.", nil
	})
}

func registerMemoryTools(server *mcp.Server, deps *Dependencies) {
	// list_traces
	server.RegisterTool("list_traces", mcp.ToolDef{
		Description: "List all memory traces with their IDs, content preview, and core status. Use this to discover trace IDs before marking them as core.",
		Properties:  map[string]mcp.PropDef{},
	}, func(ctx any, args map[string]any) (string, error) {
		traces, err := deps.GraphDB.GetAllTraces()
		if err != nil {
			return "", fmt.Errorf("failed to load traces: %w", err)
		}

		var result []map[string]any
		for _, t := range traces {
			result = append(result, map[string]any{
				"id":       t.ID,
				"content":  truncate(t.Summary, 100),
				"is_core":  t.IsCore,
				"strength": t.Strength,
			})
		}

		data, _ := json.MarshalIndent(result, "", "  ")
		return string(data), nil
	})

	// mark_core
	server.RegisterTool("mark_core", mcp.ToolDef{
		Description: "Mark a memory trace as core (part of identity) or remove core status. Core traces are always included in prompts and define Bud's identity.",
		Properties: map[string]mcp.PropDef{
			"trace_id": {Type: "string", Description: "The ID of the trace to mark as core"},
			"is_core":  {Type: "boolean", Description: "Whether to mark as core (true) or remove core status (false). Defaults to true."},
		},
		Required: []string{"trace_id"},
	}, func(ctx any, args map[string]any) (string, error) {
		traceID, ok := args["trace_id"].(string)
		if !ok {
			return "", fmt.Errorf("trace_id is required")
		}

		isCore := true
		if val, ok := args["is_core"].(bool); ok {
			isCore = val
		}

		if err := deps.GraphDB.SetTraceCore(traceID, isCore); err != nil {
			return "", err
		}

		action := "marked as core"
		if !isCore {
			action = "unmarked as core"
		}
		log.Printf("Trace %s %s", traceID, action)
		return fmt.Sprintf("Trace %s %s.", traceID, action), nil
	})

	// create_core
	server.RegisterTool("create_core", mcp.ToolDef{
		Description: "Create a new core identity trace directly. Use this to add new identity information that should always be present.",
		Properties: map[string]mcp.PropDef{
			"content": {Type: "string", Description: "The content of the core trace (e.g., 'I am Bud, a helpful assistant')"},
		},
		Required: []string{"content"},
	}, func(ctx any, args map[string]any) (string, error) {
		content, ok := args["content"].(string)
		if !ok || content == "" {
			return "", fmt.Errorf("content is required")
		}

		trace := &graph.Trace{
			ID:           fmt.Sprintf("core-%d", time.Now().UnixNano()),
			Summary:      content,
			Activation:   1.0,
			Strength:     100,
			IsCore:       true,
			CreatedAt:    time.Now(),
			LastAccessed: time.Now(),
		}

		if err := deps.GraphDB.AddTrace(trace); err != nil {
			return "", fmt.Errorf("failed to create trace: %w", err)
		}

		log.Printf("Created core trace: %s", truncate(content, 50))
		return fmt.Sprintf("Created core trace %s.", trace.ID), nil
	})

	// search_memory (if embedder is available)
	if deps.StateInspector != nil && deps.Embedder != nil {
		server.RegisterTool("search_memory", mcp.ToolDef{
			Description: "Search memory traces by semantic similarity. Returns traces most relevant to the query, optionally biased by current activation levels.",
			Properties: map[string]mcp.PropDef{
				"query":       {Type: "string", Description: "The search query"},
				"limit":       {Type: "number", Description: "Maximum results to return (default 10)"},
				"use_context": {Type: "boolean", Description: "If true, bias results toward currently activated traces (default true)"},
			},
			Required: []string{"query"},
		}, func(ctx any, args map[string]any) (string, error) {
			query, ok := args["query"].(string)
			if !ok || query == "" {
				return "", fmt.Errorf("query is required")
			}

			limit := 10
			if l, ok := args["limit"].(float64); ok && l > 0 {
				limit = int(l)
			}

			useContext := true
			if uc, ok := args["use_context"].(bool); ok {
				useContext = uc
			}

			results, err := deps.StateInspector.SearchMemoryWithContext(query, limit, useContext)
			if err != nil {
				return "", fmt.Errorf("search failed: %w", err)
			}

			data, _ := json.MarshalIndent(results, "", "  ")
			return string(data), nil
		})

		server.RegisterTool("get_trace_context", mcp.ToolDef{
			Description: "Get detailed context for a specific memory trace, including source episodes and linked entities.",
			Properties: map[string]mcp.PropDef{
				"trace_id": {Type: "string", Description: "The trace ID to get context for"},
			},
			Required: []string{"trace_id"},
		}, func(ctx any, args map[string]any) (string, error) {
			traceID, ok := args["trace_id"].(string)
			if !ok || traceID == "" {
				return "", fmt.Errorf("trace_id is required")
			}

			context, err := deps.StateInspector.GetTraceContext(traceID)
			if err != nil {
				return "", err
			}

			data, _ := json.MarshalIndent(context, "", "  ")
			return string(data), nil
		})

		server.RegisterTool("query_trace", mcp.ToolDef{
			Description: "Query a specific trace for detailed information. Returns the trace with its source episodes. Uses L1 compressed summaries by default to reduce noise while preserving context.",
			Properties: map[string]mcp.PropDef{
				"trace_id": {Type: "string", Description: "The trace ID to query"},
				"question": {Type: "string", Description: "Optional question to answer about this trace"},
				"level":    {Type: "number", Description: "Compression level for episodes: 0=raw, 1=L1 summary (default), 2=L2 summary"},
			},
			Required: []string{"trace_id"},
		}, func(ctx any, args map[string]any) (string, error) {
			traceID, ok := args["trace_id"].(string)
			if !ok || traceID == "" {
				return "", fmt.Errorf("trace_id is required")
			}

			question, _ := args["question"].(string)

			// Parse level parameter (default to 1 for L1 compression)
			level := 1
			if lvl, ok := args["level"].(float64); ok {
				level = int(lvl)
			}

			result, err := deps.StateInspector.QueryTrace(traceID, question, level)
			if err != nil {
				return "", err
			}

			return result, nil
		})

		// query_episode - query specific episode by short ID or full ID
		server.RegisterTool("query_episode", mcp.ToolDef{
			Description: "Query a specific episode by its ID (short 5-char ID or full ID). Returns the full episode details including content, author, timestamp, and summaries if available.",
			Properties: map[string]mcp.PropDef{
				"id": {Type: "string", Description: "Episode ID (short 5-char ID like 'a3f2b' or full ID)"},
			},
			Required: []string{"id"},
		}, func(ctx any, args map[string]any) (string, error) {
			id, ok := args["id"].(string)
			if !ok || id == "" {
				return "", fmt.Errorf("id is required")
			}

			var episode *graph.Episode
			var err error

			// Try short ID first (5 chars), then fall back to full ID
			if len(id) == 5 {
				episode, err = deps.GraphDB.GetEpisodeByShortID(id)
			} else {
				episode, err = deps.GraphDB.GetEpisode(id)
			}

			if err != nil {
				return "", fmt.Errorf("failed to get episode: %w", err)
			}
			if episode == nil {
				return "", fmt.Errorf("episode not found: %s", id)
			}

			// Get available summaries
			summaries := []map[string]any{}
			for level := 1; level <= 2; level++ {
				summary, _ := deps.GraphDB.GetEpisodeSummary(episode.ID, level)
				if summary != nil {
					summaries = append(summaries, map[string]any{
						"level":   summary.CompressionLevel,
						"summary": summary.Summary,
						"tokens":  summary.Tokens,
					})
				}
			}

			result := map[string]any{
				"id":        episode.ID,
				"short_id":  episode.ShortID,
				"content":   episode.Content,
				"tokens":    episode.TokenCount,
				"author":    episode.Author,
				"channel":   episode.Channel,
				"timestamp": episode.TimestampEvent.Format(time.RFC3339),
				"source":    episode.Source,
				"reply_to":  episode.ReplyTo,
			}

			if len(summaries) > 0 {
				result["summaries"] = summaries
			}

			data, _ := json.MarshalIndent(result, "", "  ")
			return string(data), nil
		})
	}
}

func registerActivityTools(server *mcp.Server, deps *Dependencies) {
	// journal_log
	server.RegisterTool("journal_log", mcp.ToolDef{
		Description: "Log a decision, action, or observation to the journal for observability. Use this to record your reasoning, decisions made, and actions taken. Helps answer 'what did you do today?' and 'why did you do that?'",
		Properties: map[string]mcp.PropDef{
			"type":      {Type: "string", Description: "Entry type: 'decision', 'impulse', 'reflex', 'exploration', 'action', or 'observation'"},
			"summary":   {Type: "string", Description: "Brief description of what happened"},
			"context":   {Type: "string", Description: "What prompted this (optional)"},
			"reasoning": {Type: "string", Description: "Why this decision was made (optional)"},
			"outcome":   {Type: "string", Description: "What resulted from this (optional)"},
		},
		Required: []string{"summary"},
	}, func(ctx any, args map[string]any) (string, error) {
		summary, _ := args["summary"].(string)
		context, _ := args["context"].(string)
		reasoning, _ := args["reasoning"].(string)
		outcome, _ := args["outcome"].(string)

		if summary == "" {
			return "", fmt.Errorf("summary is required")
		}

		entryType, _ := args["type"].(string)
		if entryType == "" {
			entryType = "observation"
		}

		deps.ActivityLog.Log(activity.Entry{
			Type:    activity.Type(entryType),
			Summary: summary,
			Data: map[string]any{
				"context":   context,
				"reasoning": reasoning,
				"outcome":   outcome,
			},
		})

		log.Printf("Journal logged: [%s] %s", entryType, truncate(summary, 50))
		return "Journal entry recorded.", nil
	})

	// journal_recent
	server.RegisterTool("journal_recent", mcp.ToolDef{
		Description: "Get recent journal entries. Use this to review what you've been doing and why.",
		Properties: map[string]mcp.PropDef{
			"count": {Type: "number", Description: "Number of entries to return (default 20)"},
		},
	}, func(ctx any, args map[string]any) (string, error) {
		count := 20
		if c, ok := args["count"].(float64); ok && c > 0 {
			count = int(c)
		}

		entries, err := deps.ActivityLog.Recent(count)
		if err != nil {
			return "", fmt.Errorf("failed to get recent entries: %w", err)
		}

		data, _ := json.MarshalIndent(entries, "", "  ")
		return string(data), nil
	})

	// journal_today
	server.RegisterTool("journal_today", mcp.ToolDef{
		Description: "Get today's journal entries. Use this to answer 'what did you do today?'",
		Properties:  map[string]mcp.PropDef{},
	}, func(ctx any, args map[string]any) (string, error) {
		entries, err := deps.ActivityLog.Today()
		if err != nil {
			return "", fmt.Errorf("failed to get today's entries: %w", err)
		}

		data, _ := json.MarshalIndent(entries, "", "  ")
		return string(data), nil
	})

	// activity_recent
	server.RegisterTool("activity_recent", mcp.ToolDef{
		Description: "Get recent activity entries from the log.",
		Properties: map[string]mcp.PropDef{
			"count": {Type: "number", Description: "Number of entries to return (default 50)"},
		},
	}, func(ctx any, args map[string]any) (string, error) {
		count := 50
		if c, ok := args["count"].(float64); ok && c > 0 {
			count = int(c)
		}

		entries, err := deps.ActivityLog.Recent(count)
		if err != nil {
			return "", fmt.Errorf("failed to get recent activity: %w", err)
		}

		data, _ := json.MarshalIndent(entries, "", "  ")
		return string(data), nil
	})

	// activity_today
	server.RegisterTool("activity_today", mcp.ToolDef{
		Description: "Get today's activity entries.",
		Properties:  map[string]mcp.PropDef{},
	}, func(ctx any, args map[string]any) (string, error) {
		entries, err := deps.ActivityLog.Today()
		if err != nil {
			return "", fmt.Errorf("failed to get today's activity: %w", err)
		}

		data, _ := json.MarshalIndent(entries, "", "  ")
		return string(data), nil
	})

	// activity_search
	server.RegisterTool("activity_search", mcp.ToolDef{
		Description: "Search activity entries by text.",
		Properties: map[string]mcp.PropDef{
			"query": {Type: "string", Description: "Text to search for"},
			"limit": {Type: "number", Description: "Maximum entries to return (default 100)"},
		},
		Required: []string{"query"},
	}, func(ctx any, args map[string]any) (string, error) {
		query, ok := args["query"].(string)
		if !ok || query == "" {
			return "", fmt.Errorf("query is required")
		}

		limit := 100
		if l, ok := args["limit"].(float64); ok && l > 0 {
			limit = int(l)
		}

		entries, err := deps.ActivityLog.Search(query, limit)
		if err != nil {
			return "", fmt.Errorf("search failed: %w", err)
		}

		data, _ := json.MarshalIndent(entries, "", "  ")
		return string(data), nil
	})

	// activity_by_type
	server.RegisterTool("activity_by_type", mcp.ToolDef{
		Description: "Filter activity entries by event type.",
		Properties: map[string]mcp.PropDef{
			"type":  {Type: "string", Description: "Event type: input, reflex, reflex_pass, executive_wake, executive_done, action, decision, error"},
			"limit": {Type: "number", Description: "Maximum entries to return (default 50)"},
		},
	}, func(ctx any, args map[string]any) (string, error) {
		eventType, _ := args["type"].(string)

		limit := 50
		if l, ok := args["limit"].(float64); ok && l > 0 {
			limit = int(l)
		}

		entries, err := deps.ActivityLog.ByType(activity.Type(eventType), limit)
		if err != nil {
			return "", fmt.Errorf("failed to filter by type: %w", err)
		}

		data, _ := json.MarshalIndent(entries, "", "  ")
		return string(data), nil
	})
}

func registerMotivationTools(server *mcp.Server, deps *Dependencies) {
	if deps.TaskStore == nil || deps.IdeaStore == nil {
		return
	}

	// add_bud_task
	server.RegisterTool("add_bud_task", mcp.ToolDef{
		Description: "Add a task (Bud's commitment) to your task queue. Use this to track things you've committed to do.",
		Properties: map[string]mcp.PropDef{
			"task":     {Type: "string", Description: "What you need to do"},
			"context":  {Type: "string", Description: "Why this task exists (optional)"},
			"priority": {Type: "number", Description: "Priority level: 1=highest, 2=medium, 3=low (default 2)"},
			"due":      {Type: "string", Description: "Due date/time in RFC3339 format (optional)"},
		},
		Required: []string{"task"},
	}, func(ctx any, args map[string]any) (string, error) {
		taskDesc, ok := args["task"].(string)
		if !ok || taskDesc == "" {
			return "", fmt.Errorf("task is required")
		}

		priority := 2
		if p, ok := args["priority"].(float64); ok {
			priority = int(p)
		}

		task := &motivation.Task{
			ID:       fmt.Sprintf("task-%d", time.Now().UnixNano()),
			Task:     taskDesc,
			Priority: priority,
			Status:   "pending",
		}

		if taskContext, ok := args["context"].(string); ok {
			task.Context = taskContext
		}

		if due, ok := args["due"].(string); ok && due != "" {
			if t, err := time.Parse(time.RFC3339, due); err == nil {
				task.Due = &t
			}
		}

		deps.TaskStore.Add(task)
		if err := deps.TaskStore.Save(); err != nil {
			return "", fmt.Errorf("failed to save task: %w", err)
		}

		log.Printf("Added task: %s", truncate(taskDesc, 50))
		return fmt.Sprintf("Task added: %s", task.ID), nil
	})

	// list_bud_tasks
	server.RegisterTool("list_bud_tasks", mcp.ToolDef{
		Description: "List pending Bud tasks. Use this to see what you've committed to do.",
		Properties:  map[string]mcp.PropDef{},
	}, func(ctx any, args map[string]any) (string, error) {
		deps.TaskStore.Load()
		tasks := deps.TaskStore.GetPending()
		data, _ := json.MarshalIndent(tasks, "", "  ")
		return string(data), nil
	})

	// complete_bud_task
	server.RegisterTool("complete_bud_task", mcp.ToolDef{
		Description: "Mark a Bud task as complete.",
		Properties: map[string]mcp.PropDef{
			"task_id": {Type: "string", Description: "ID of the task to complete"},
		},
		Required: []string{"task_id"},
	}, func(ctx any, args map[string]any) (string, error) {
		taskID, ok := args["task_id"].(string)
		if !ok || taskID == "" {
			return "", fmt.Errorf("task_id is required")
		}

		// Get task first to return its description
		task := deps.TaskStore.Get(taskID)
		if task == nil {
			return "", fmt.Errorf("task not found: %s", taskID)
		}
		taskDesc := task.Task

		deps.TaskStore.Complete(taskID)

		if err := deps.TaskStore.Save(); err != nil {
			return "", fmt.Errorf("failed to save: %w", err)
		}

		log.Printf("Completed task: %s", taskID)
		return fmt.Sprintf("Task completed: %s", taskDesc), nil
	})

	// add_idea
	server.RegisterTool("add_idea", mcp.ToolDef{
		Description: "Save an idea for later exploration. Ideas are things you want to learn or think about when idle.",
		Properties: map[string]mcp.PropDef{
			"idea":       {Type: "string", Description: "The idea or topic to explore"},
			"sparked_by": {Type: "string", Description: "What triggered this idea (optional)"},
			"priority":   {Type: "number", Description: "Interest level: 1=highest, 2=medium, 3=low (default 2)"},
		},
		Required: []string{"idea"},
	}, func(ctx any, args map[string]any) (string, error) {
		ideaDesc, ok := args["idea"].(string)
		if !ok || ideaDesc == "" {
			return "", fmt.Errorf("idea is required")
		}

		priority := 2
		if p, ok := args["priority"].(float64); ok {
			priority = int(p)
		}

		idea := &motivation.Idea{
			ID:       fmt.Sprintf("idea-%d", time.Now().UnixNano()),
			Idea:     ideaDesc,
			Priority: priority,
			Added:    time.Now(),
		}

		if sparkedBy, ok := args["sparked_by"].(string); ok {
			idea.SparkBy = sparkedBy
		}

		deps.IdeaStore.Add(idea)
		if err := deps.IdeaStore.Save(); err != nil {
			return "", fmt.Errorf("failed to save idea: %w", err)
		}

		log.Printf("Added idea: %s", truncate(ideaDesc, 50))
		return fmt.Sprintf("Idea saved: %s", idea.ID), nil
	})

	// list_ideas
	server.RegisterTool("list_ideas", mcp.ToolDef{
		Description: "List unexplored ideas. Use this to find something to think about during idle time.",
		Properties:  map[string]mcp.PropDef{},
	}, func(ctx any, args map[string]any) (string, error) {
		deps.IdeaStore.Load()
		ideas := deps.IdeaStore.GetUnexplored()
		data, _ := json.MarshalIndent(ideas, "", "  ")
		return string(data), nil
	})

	// explore_idea
	server.RegisterTool("explore_idea", mcp.ToolDef{
		Description: "Mark an idea as explored, with notes about what you learned.",
		Properties: map[string]mcp.PropDef{
			"idea_id": {Type: "string", Description: "ID of the idea that was explored"},
			"notes":   {Type: "string", Description: "What you learned or discovered (optional)"},
		},
		Required: []string{"idea_id"},
	}, func(ctx any, args map[string]any) (string, error) {
		ideaID, ok := args["idea_id"].(string)
		if !ok || ideaID == "" {
			return "", fmt.Errorf("idea_id is required")
		}

		notes, _ := args["notes"].(string)

		idea := deps.IdeaStore.Get(ideaID)
		if idea == nil {
			return "", fmt.Errorf("idea not found: %s", ideaID)
		}
		deps.IdeaStore.MarkExplored(ideaID, notes)

		if err := deps.IdeaStore.Save(); err != nil {
			return "", fmt.Errorf("failed to save: %w", err)
		}

		log.Printf("Explored idea: %s", ideaID)
		return fmt.Sprintf("Idea marked as explored: %s", idea.Idea), nil
	})
}

func registerStateTools(server *mcp.Server, deps *Dependencies) {
	if deps.StateInspector == nil {
		return
	}

	// state_summary
	server.RegisterTool("state_summary", mcp.ToolDef{
		Description: "Get summary of all state components (traces, percepts, threads, logs, queues).",
		Properties:  map[string]mcp.PropDef{},
	}, func(ctx any, args map[string]any) (string, error) {
		summary, err := deps.StateInspector.Summary()
		if err != nil {
			return "", err
		}
		data, _ := json.MarshalIndent(summary, "", "  ")
		return string(data), nil
	})

	// state_health
	server.RegisterTool("state_health", mcp.ToolDef{
		Description: "Run health checks on state and get recommendations for cleanup.",
		Properties:  map[string]mcp.PropDef{},
	}, func(ctx any, args map[string]any) (string, error) {
		health, err := deps.StateInspector.Health()
		if err != nil {
			return "", err
		}
		data, _ := json.MarshalIndent(health, "", "  ")
		return string(data), nil
	})

	// state_traces
	server.RegisterTool("state_traces", mcp.ToolDef{
		Description: "Manage memory traces. Actions: list, show, delete, clear, regen_core.",
		Properties: map[string]mcp.PropDef{
			"action":     {Type: "string", Description: "Action: list (default), show, delete, clear, regen_core"},
			"id":         {Type: "string", Description: "Trace ID (for show/delete)"},
			"clear_core": {Type: "boolean", Description: "If true with clear action, clears core traces instead of non-core"},
		},
	}, func(ctx any, args map[string]any) (string, error) {
		action, _ := args["action"].(string)
		if action == "" {
			action = "list"
		}

		switch action {
		case "list":
			traces, err := deps.StateInspector.ListTraces()
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
			trace, err := deps.StateInspector.GetTrace(id)
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
			if err := deps.StateInspector.DeleteTrace(id); err != nil {
				return "", err
			}
			return fmt.Sprintf("Deleted trace: %s", id), nil

		case "clear":
			clearCore, _ := args["clear_core"].(bool)
			count, err := deps.StateInspector.ClearTraces(clearCore)
			if err != nil {
				return "", err
			}
			if clearCore {
				return fmt.Sprintf("Cleared %d core traces", count), nil
			}
			return fmt.Sprintf("Cleared %d non-core traces", count), nil

		case "regen_core":
			seedPath := "seed/core_seed.md"
			count, err := deps.StateInspector.RegenCore(seedPath)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("Regenerated %d core traces", count), nil

		default:
			return "", fmt.Errorf("unknown action: %s", action)
		}
	})

	// state_sessions
	server.RegisterTool("state_sessions", mcp.ToolDef{
		Description: "Manage session tracking. Actions: list, clear.",
		Properties: map[string]mcp.PropDef{
			"action": {Type: "string", Description: "Action: list (default), clear"},
		},
	}, func(ctx any, args map[string]any) (string, error) {
		action, _ := args["action"].(string)
		if action == "" {
			action = "list"
		}

		switch action {
		case "list":
			sessions, err := deps.StateInspector.ListSessions()
			if err != nil {
				return "", err
			}
			data, _ := json.MarshalIndent(sessions, "", "  ")
			return string(data), nil

		case "clear":
			if err := deps.StateInspector.ClearSessions(); err != nil {
				return "", err
			}
			return "Cleared sessions", nil

		default:
			return "", fmt.Errorf("unknown action: %s", action)
		}
	})

	// state_regen_core
	server.RegisterTool("state_regen_core", mcp.ToolDef{
		Description: "Regenerate core identity traces from core_seed.md. Clears existing core traces first.",
		Properties:  map[string]mcp.PropDef{},
	}, func(ctx any, args map[string]any) (string, error) {
		seedPath := "seed/core_seed.md"
		count, err := deps.StateInspector.RegenCore(seedPath)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Regenerated %d core traces from %s", count, seedPath), nil
	})

	// memory_flush
	server.RegisterTool("memory_flush", mcp.ToolDef{
		Description: "Flush the conversation buffer to memory. Clears conversation context but keeps session running. Pending thoughts will be extracted by main process.",
		Properties:  map[string]mcp.PropDef{},
	}, func(ctx any, args map[string]any) (string, error) {
		// Trigger consolidation in main process
		triggerPath := filepath.Join(deps.StatePath, "consolidate.trigger")
		if err := os.WriteFile(triggerPath, []byte(time.Now().Format(time.RFC3339)), 0644); err != nil {
			log.Printf("Warning: failed to write consolidation trigger: %v", err)
		}

		log.Printf("Memory flush: triggered consolidation")
		return "Memory flushed. Consolidation triggered.", nil
	})

	// memory_reset - full session reset with coordination
	server.RegisterTool("memory_reset", mcp.ToolDef{
		Description: "Reset memory completely. Clears conversation buffer AND signals the session to end. Use for clean slate testing.",
		Properties:  map[string]mcp.PropDef{},
	}, func(ctx any, args map[string]any) (string, error) {
		// Write reset pending flag to prevent race conditions
		resetPendingPath := filepath.Join(deps.StatePath, "reset.pending")
		if err := os.WriteFile(resetPendingPath, []byte(time.Now().Format(time.RFC3339)), 0644); err != nil {
			log.Printf("Warning: failed to write reset pending flag: %v", err)
		}
		log.Printf("Memory reset: set reset.pending flag to block new sessions")

		// Trigger consolidation
		triggerPath := filepath.Join(deps.StatePath, "consolidate.trigger")
		if err := os.WriteFile(triggerPath, []byte(time.Now().Format(time.RFC3339)), 0644); err != nil {
			log.Printf("Warning: failed to write consolidation trigger: %v", err)
		}

		// Signal main process to clear in-memory buffer
		bufferClearPath := filepath.Join(deps.StatePath, "buffer.clear")
		if err := os.WriteFile(bufferClearPath, []byte(time.Now().Format(time.RFC3339)), 0644); err != nil {
			log.Printf("Warning: failed to write buffer clear trigger: %v", err)
		}

		// Give consolidation and buffer clear a moment to process
		time.Sleep(3 * time.Second)

		// Clear conversation buffer on disk
		bufferPath := filepath.Join(deps.SystemPath, "buffers.json")
		if err := os.WriteFile(bufferPath, []byte("{}"), 0644); err != nil {
			return "", fmt.Errorf("failed to clear buffer: %w", err)
		}

		// Signal session reset via inbox
		msg := map[string]any{
			"id":        fmt.Sprintf("reset-%d", time.Now().UnixNano()),
			"type":      "signal",
			"subtype":   "reset_session",
			"content":   "Memory reset requested",
			"timestamp": time.Now().Format(time.RFC3339),
			"status":    "pending",
		}

		inboxPath := filepath.Join(deps.QueuesPath, "inbox.jsonl")
		f, err := os.OpenFile(inboxPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return "", fmt.Errorf("failed to open inbox: %w", err)
		}
		defer f.Close()

		data, _ := json.Marshal(msg)
		if _, err := f.WriteString(string(data) + "\n"); err != nil {
			return "", fmt.Errorf("failed to write reset signal: %w", err)
		}

		log.Printf("Memory reset: buffer cleared, reset signal sent")
		return "Memory reset complete. Session will end.", nil
	})

	// trigger_redeploy - allows bud to request redeployment
	server.RegisterTool("trigger_redeploy", mcp.ToolDef{
		Description: "Trigger a redeployment of the bud service. Use this after code changes have been pushed.",
		Properties: map[string]mcp.PropDef{
			"reason": {Type: "string", Description: "Reason for redeployment (optional)"},
		},
	}, func(ctx any, args map[string]any) (string, error) {
		reason, _ := args["reason"].(string)
		if reason == "" {
			reason = "Redeploy requested"
		}

		// Find deploy script relative to state path
		budDir := filepath.Dir(deps.StatePath)
		if budDir == "." {
			budDir = "."
		}
		deployScript := filepath.Join(budDir, "deploy", "deploy.sh")
		if _, err := os.Stat(deployScript); os.IsNotExist(err) {
			return "", fmt.Errorf("deploy script not found: %s", deployScript)
		}

		// Run deploy.sh in background so we can return before being killed
		cmd := exec.Command("bash", "-c", fmt.Sprintf("nohup %s > /dev/null 2>&1 &", deployScript))
		if err := cmd.Start(); err != nil {
			return "", fmt.Errorf("failed to start deploy: %w", err)
		}

		log.Printf("Redeploy triggered: %s", reason)
		return "Redeploy started. Service will restart momentarily.", nil
	})

	// state_percepts - manage percepts
	server.RegisterTool("state_percepts", mcp.ToolDef{
		Description: "Manage percepts (short-term memory). Actions: list, count, clear.",
		Properties: map[string]mcp.PropDef{
			"action":     {Type: "string", Description: "Action: list (default), count, clear"},
			"older_than": {Type: "string", Description: "Duration for clear (e.g., '1h', '30m'). If omitted, clears all."},
		},
	}, func(ctx any, args map[string]any) (string, error) {
		action, _ := args["action"].(string)
		if action == "" {
			action = "list"
		}

		switch action {
		case "list":
			percepts, err := deps.StateInspector.ListPercepts()
			if err != nil {
				return "", err
			}
			data, _ := json.MarshalIndent(percepts, "", "  ")
			return string(data), nil

		case "count":
			percepts, err := deps.StateInspector.ListPercepts()
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
			count, err := deps.StateInspector.ClearPercepts(dur)
			if err != nil {
				return "", err
			}
			if olderThan != "" {
				return fmt.Sprintf("Cleared %d percepts older than %s", count, olderThan), nil
			}
			return "Cleared all percepts", nil

		default:
			return "", fmt.Errorf("unknown action: %s", action)
		}
	})

	// state_threads - manage threads
	server.RegisterTool("state_threads", mcp.ToolDef{
		Description: "Manage threads (working memory). Actions: list, show, clear.",
		Properties: map[string]mcp.PropDef{
			"action": {Type: "string", Description: "Action: list (default), show, clear"},
			"id":     {Type: "string", Description: "Thread ID (for show)"},
			"status": {Type: "string", Description: "Filter for clear (active, paused, frozen, complete)"},
		},
	}, func(ctx any, args map[string]any) (string, error) {
		action, _ := args["action"].(string)
		if action == "" {
			action = "list"
		}

		switch action {
		case "list":
			threads, err := deps.StateInspector.ListThreads()
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
			thread, err := deps.StateInspector.GetThread(id)
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
			count, err := deps.StateInspector.ClearThreads(statusPtr)
			if err != nil {
				return "", err
			}
			if statusStr != "" {
				return fmt.Sprintf("Cleared %d threads with status %s", count, statusStr), nil
			}
			return "Cleared all threads", nil

		default:
			return "", fmt.Errorf("unknown action: %s", action)
		}
	})

	// state_logs - manage logs
	server.RegisterTool("state_logs", mcp.ToolDef{
		Description: "Manage journal and activity logs. Actions: tail, truncate.",
		Properties: map[string]mcp.PropDef{
			"action": {Type: "string", Description: "Action: tail (default), truncate"},
			"count":  {Type: "number", Description: "Number of entries for tail (default 20)"},
			"keep":   {Type: "number", Description: "Entries to keep for truncate (default 100)"},
		},
	}, func(ctx any, args map[string]any) (string, error) {
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
			entries, err := deps.StateInspector.TailLogs(count)
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
			if err := deps.StateInspector.TruncateLogs(keep); err != nil {
				return "", err
			}
			return fmt.Sprintf("Truncated logs to %d entries", keep), nil

		default:
			return "", fmt.Errorf("unknown action: %s", action)
		}
	})

	// state_queues - manage queues
	server.RegisterTool("state_queues", mcp.ToolDef{
		Description: "Manage message queues (inbox, outbox, signals). Actions: list, clear.",
		Properties: map[string]mcp.PropDef{
			"action": {Type: "string", Description: "Action: list (default), clear"},
		},
	}, func(ctx any, args map[string]any) (string, error) {
		action, _ := args["action"].(string)
		if action == "" {
			action = "list"
		}

		switch action {
		case "list":
			queues, err := deps.StateInspector.ListQueues()
			if err != nil {
				return "", err
			}
			data, _ := json.MarshalIndent(queues, "", "  ")
			return string(data), nil

		case "clear":
			if err := deps.StateInspector.ClearQueues(); err != nil {
				return "", err
			}
			return "Cleared all queues", nil

		default:
			return "", fmt.Errorf("unknown action: %s", action)
		}
	})
}

func registerGTDTools(server *mcp.Server, deps *Dependencies) {
	// gtd_add
	server.RegisterTool("gtd_add", mcp.ToolDef{
		Description: "Add a task to the user's GTD system. Quick capture to inbox by default, or specify when/project to place it directly.",
		Properties: map[string]mcp.PropDef{
			"title":   {Type: "string", Description: "Task title (what needs to be done)"},
			"notes":   {Type: "string", Description: "Additional notes or context for the task (optional)"},
			"when":    {Type: "string", Description: "When to do it: inbox (default), today, anytime, someday, or YYYY-MM-DD date"},
			"project": {Type: "string", Description: "Project ID to add task to (optional)"},
			"area":    {Type: "string", Description: "Area ID for the task (optional, only if not in a project)"},
			"heading": {Type: "string", Description: "Heading name within the project (requires project)"},
		},
		Required: []string{"title"},
	}, func(ctx any, args map[string]any) (string, error) {
		title, ok := args["title"].(string)
		if !ok || title == "" {
			return "", fmt.Errorf("title is required")
		}

		task := &gtd.Task{
			Title: title,
		}

		if notes, ok := args["notes"].(string); ok {
			task.Notes = notes
		}
		if when, ok := args["when"].(string); ok {
			task.When = when
		}
		if project, ok := args["project"].(string); ok {
			task.Project = project
		}
		if area, ok := args["area"].(string); ok {
			task.Area = area
		}
		if heading, ok := args["heading"].(string); ok {
			task.Heading = heading
		}

		deps.GTDStore.AddTask(task)
		if err := deps.GTDStore.Save(); err != nil {
			return "", fmt.Errorf("failed to save task: %w", err)
		}

		log.Printf("GTD task added: %s", truncate(title, 50))
		return fmt.Sprintf("Task added to GTD: %s (ID: %s)", title, task.ID), nil
	})

	// gtd_list
	server.RegisterTool("gtd_list", mcp.ToolDef{
		Description: "List tasks from the user's GTD system with optional filters.",
		Properties: map[string]mcp.PropDef{
			"when":    {Type: "string", Description: "Filter by when: inbox, today, anytime, someday, logbook (completed+canceled), or YYYY-MM-DD date"},
			"project": {Type: "string", Description: "Filter by project ID"},
			"area":    {Type: "string", Description: "Filter by area ID"},
			"status":  {Type: "string", Description: "Filter by status: open (default), completed, canceled, or all"},
		},
	}, func(ctx any, args map[string]any) (string, error) {
		when, _ := args["when"].(string)
		projectID, _ := args["project"].(string)
		areaID, _ := args["area"].(string)

		tasks := deps.GTDStore.GetTasks(when, projectID, areaID)

		// Filter by status if specified
		if status, ok := args["status"].(string); ok && status != "" && status != "all" {
			var filtered []gtd.Task
			for _, t := range tasks {
				if t.Status == status {
					filtered = append(filtered, t)
				}
			}
			tasks = filtered
		}

		data, _ := json.MarshalIndent(tasks, "", "  ")
		return string(data), nil
	})

	// gtd_complete
	server.RegisterTool("gtd_complete", mcp.ToolDef{
		Description: "Mark a task as complete in the user's GTD system. Handles repeating tasks by creating the next occurrence.",
		Properties: map[string]mcp.PropDef{
			"id": {Type: "string", Description: "Task ID to mark as complete (required)"},
		},
		Required: []string{"id"},
	}, func(ctx any, args map[string]any) (string, error) {
		id, ok := args["id"].(string)
		if !ok || id == "" {
			return "", fmt.Errorf("id is required")
		}

		if err := deps.GTDStore.CompleteTask(id); err != nil {
			return "", fmt.Errorf("failed to complete task: %w", err)
		}

		log.Printf("GTD task completed: %s", id)
		return fmt.Sprintf("Task completed: %s", id), nil
	})

	// gtd_update
	server.RegisterTool("gtd_update", mcp.ToolDef{
		Description: "Update a task in the user's GTD system. Only provided fields are updated.",
		Properties: map[string]mcp.PropDef{
			"id":        {Type: "string", Description: "Task ID to update (required)"},
			"title":     {Type: "string", Description: "New title for the task"},
			"notes":     {Type: "string", Description: "New notes for the task"},
			"when":      {Type: "string", Description: "When to do it: inbox, today, anytime, someday, or YYYY-MM-DD date"},
			"project":   {Type: "string", Description: "Project ID to move task to (empty string to remove from project)"},
			"heading":   {Type: "string", Description: "Heading name within the project"},
			"area":      {Type: "string", Description: "Area ID for the task (empty string to remove area)"},
			"checklist": {Type: "array", Description: "Checklist items as array of {text, done} objects"},
		},
		Required: []string{"id"},
	}, func(ctx any, args map[string]any) (string, error) {
		id, ok := args["id"].(string)
		if !ok || id == "" {
			return "", fmt.Errorf("id is required")
		}

		task := deps.GTDStore.GetTask(id)
		if task == nil {
			return "", fmt.Errorf("task not found: %s", id)
		}

		var updates []string

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
		if checklist, ok := args["checklist"].([]any); ok {
			var items []gtd.ChecklistItem
			for _, item := range checklist {
				if itemMap, ok := item.(map[string]any); ok {
					ci := gtd.ChecklistItem{}
					if text, ok := itemMap["text"].(string); ok {
						ci.Text = text
					}
					if done, ok := itemMap["done"].(bool); ok {
						ci.Done = done
					}
					items = append(items, ci)
				}
			}
			task.Checklist = items
			updates = append(updates, "checklist")
		}

		if err := deps.GTDStore.UpdateTask(task); err != nil {
			return "", fmt.Errorf("failed to update task: %w", err)
		}
		if err := deps.GTDStore.Save(); err != nil {
			return "", fmt.Errorf("failed to save: %w", err)
		}

		log.Printf("GTD task updated: %s (%v)", id, updates)
		return fmt.Sprintf("Task updated: %s (fields: %s)", task.Title, strings.Join(updates, ", ")), nil
	})

	// gtd_areas
	server.RegisterTool("gtd_areas", mcp.ToolDef{
		Description: "Manage areas of responsibility in the user's GTD system. Areas are high-level categories like Work, Home, Health.",
		Properties: map[string]mcp.PropDef{
			"action": {Type: "string", Description: "Action to perform: list, add, or update"},
			"id":     {Type: "string", Description: "Area ID (required for update)"},
			"title":  {Type: "string", Description: "Area title (required for add, optional for update)"},
		},
		Required: []string{"action"},
	}, func(ctx any, args map[string]any) (string, error) {
		action, _ := args["action"].(string)
		if action == "" {
			return "", fmt.Errorf("action is required (list, add, or update)")
		}

		switch action {
		case "list":
			areas := deps.GTDStore.GetAreas()
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
			deps.GTDStore.AddArea(area)
			if err := deps.GTDStore.Save(); err != nil {
				return "", fmt.Errorf("failed to save: %w", err)
			}

			log.Printf("GTD area added: %s", title)
			return fmt.Sprintf("Area added: %s (ID: %s)", title, area.ID), nil

		case "update":
			id, ok := args["id"].(string)
			if !ok || id == "" {
				return "", fmt.Errorf("id is required for update")
			}

			area := deps.GTDStore.GetArea(id)
			if area == nil {
				return "", fmt.Errorf("area not found: %s", id)
			}

			if title, ok := args["title"].(string); ok {
				area.Title = title
			}

			if err := deps.GTDStore.UpdateArea(area); err != nil {
				return "", fmt.Errorf("failed to update area: %w", err)
			}
			if err := deps.GTDStore.Save(); err != nil {
				return "", fmt.Errorf("failed to save: %w", err)
			}

			log.Printf("GTD area updated: %s", id)
			return fmt.Sprintf("Area updated: %s", area.Title), nil

		default:
			return "", fmt.Errorf("unknown action: %s", action)
		}
	})

	// gtd_projects
	server.RegisterTool("gtd_projects", mcp.ToolDef{
		Description: "Manage projects in the user's GTD system. Projects are multi-step outcomes with tasks.",
		Properties: map[string]mcp.PropDef{
			"action":   {Type: "string", Description: "Action to perform: list, add, or update"},
			"id":       {Type: "string", Description: "Project ID (required for update)"},
			"title":    {Type: "string", Description: "Project title (required for add, optional for update)"},
			"notes":    {Type: "string", Description: "Project notes (optional)"},
			"when":     {Type: "string", Description: "When: anytime, someday, or YYYY-MM-DD date (optional)"},
			"area":     {Type: "string", Description: "Area ID for filtering (list) or assignment (add/update)"},
			"status":   {Type: "string", Description: "Filter by status (list only): open (default), completed, canceled, or all"},
			"headings": {Type: "array", Description: "Ordered list of heading names for organizing tasks (optional)"},
		},
		Required: []string{"action"},
	}, func(ctx any, args map[string]any) (string, error) {
		action, _ := args["action"].(string)
		if action == "" {
			return "", fmt.Errorf("action is required (list, add, or update)")
		}

		switch action {
		case "list":
			when, _ := args["when"].(string)
			area, _ := args["area"].(string)
			status, _ := args["status"].(string)
			if status == "" {
				status = "open"
			}

			projects := deps.GTDStore.GetProjects(when, area)

			// Filter by status
			if status != "all" {
				var filtered []gtd.Project
				for _, p := range projects {
					if p.Status == status {
						filtered = append(filtered, p)
					}
				}
				projects = filtered
			}

			if len(projects) == 0 {
				return "No projects found.", nil
			}
			data, _ := json.MarshalIndent(projects, "", "  ")
			return string(data), nil

		case "add":
			title, ok := args["title"].(string)
			if !ok || title == "" {
				return "", fmt.Errorf("title is required for add")
			}

			project := &gtd.Project{
				Title:  title,
				Status: "open",
			}

			if notes, ok := args["notes"].(string); ok {
				project.Notes = notes
			}
			if when, ok := args["when"].(string); ok {
				project.When = when
			}
			if area, ok := args["area"].(string); ok {
				project.Area = area
			}
			if headings, ok := args["headings"].([]any); ok {
				for _, h := range headings {
					if s, ok := h.(string); ok {
						project.Headings = append(project.Headings, s)
					}
				}
			}

			deps.GTDStore.AddProject(project)
			if err := deps.GTDStore.Save(); err != nil {
				return "", fmt.Errorf("failed to save: %w", err)
			}

			log.Printf("GTD project added: %s", title)
			return fmt.Sprintf("Project added: %s (ID: %s)", title, project.ID), nil

		case "update":
			id, ok := args["id"].(string)
			if !ok || id == "" {
				return "", fmt.Errorf("id is required for update")
			}

			project := deps.GTDStore.GetProject(id)
			if project == nil {
				return "", fmt.Errorf("project not found: %s", id)
			}

			var updates []string
			if title, ok := args["title"].(string); ok {
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
			if headings, ok := args["headings"].([]any); ok {
				project.Headings = nil
				for _, h := range headings {
					if s, ok := h.(string); ok {
						project.Headings = append(project.Headings, s)
					}
				}
				updates = append(updates, "headings")
			}

			if err := deps.GTDStore.UpdateProject(project); err != nil {
				return "", fmt.Errorf("failed to update project: %w", err)
			}
			if err := deps.GTDStore.Save(); err != nil {
				return "", fmt.Errorf("failed to save: %w", err)
			}

			log.Printf("GTD project updated: %s (%v)", id, updates)
			return fmt.Sprintf("Project updated: %s (fields: %s)", project.Title, strings.Join(updates, ", ")), nil

		default:
			return "", fmt.Errorf("unknown action: %s", action)
		}
	})
}

func registerReflexTools(server *mcp.Server, deps *Dependencies) {
	// create_reflex
	server.RegisterTool("create_reflex", mcp.ToolDef{
		Description: "Create a new reflex (automated response). Reflexes run without waking the executive for pattern-matched inputs.",
		Properties: map[string]mcp.PropDef{
			"name":        {Type: "string", Description: "Unique name for the reflex"},
			"description": {Type: "string", Description: "What this reflex does"},
			"pattern":     {Type: "string", Description: "Regex pattern to match (use capture groups for extraction)"},
			"extract":     {Type: "array", Description: "Names for captured groups (e.g., [\"url\", \"title\"])"},
			"pipeline":    {Type: "array", Description: "Array of action steps: [{action, input, output, ...params}]"},
		},
		Required: []string{"name", "pipeline"},
	}, func(ctx any, args map[string]any) (string, error) {
		name, ok := args["name"].(string)
		if !ok || name == "" {
			return "", fmt.Errorf("name is required")
		}

		r := &reflex.Reflex{
			Name: name,
		}

		if desc, ok := args["description"].(string); ok {
			r.Description = desc
		}
		if pattern, ok := args["pattern"].(string); ok {
			r.Trigger.Pattern = pattern
		}
		if extract, ok := args["extract"].([]any); ok {
			for _, e := range extract {
				if s, ok := e.(string); ok {
					r.Trigger.Extract = append(r.Trigger.Extract, s)
				}
			}
		}

		// Parse pipeline
		if pipeline, ok := args["pipeline"].([]any); ok {
			for _, step := range pipeline {
				if stepMap, ok := step.(map[string]any); ok {
					pipelineStep := reflex.PipelineStep{
						Params: make(map[string]any),
					}
					if a, ok := stepMap["action"].(string); ok {
						pipelineStep.Action = a
					}
					if input, ok := stepMap["input"].(string); ok {
						pipelineStep.Input = input
					}
					if output, ok := stepMap["output"].(string); ok {
						pipelineStep.Output = output
					}
					// Copy remaining params
					for k, v := range stepMap {
						if k != "action" && k != "input" && k != "output" {
							pipelineStep.Params[k] = v
						}
					}
					r.Pipeline = append(r.Pipeline, pipelineStep)
				}
			}
		}

		if err := deps.ReflexEngine.SaveReflex(r); err != nil {
			return "", fmt.Errorf("failed to create reflex: %w", err)
		}

		log.Printf("Created reflex: %s", name)
		return fmt.Sprintf("Reflex created: %s", name), nil
	})

	// list_reflexes
	server.RegisterTool("list_reflexes", mcp.ToolDef{
		Description: "List all defined reflexes.",
		Properties:  map[string]mcp.PropDef{},
	}, func(ctx any, args map[string]any) (string, error) {
		reflexes := deps.ReflexEngine.List()
		data, _ := json.MarshalIndent(reflexes, "", "  ")
		return string(data), nil
	})

	// delete_reflex
	server.RegisterTool("delete_reflex", mcp.ToolDef{
		Description: "Delete a reflex by name.",
		Properties: map[string]mcp.PropDef{
			"name": {Type: "string", Description: "Name of the reflex to delete"},
		},
		Required: []string{"name"},
	}, func(ctx any, args map[string]any) (string, error) {
		name, ok := args["name"].(string)
		if !ok || name == "" {
			return "", fmt.Errorf("name is required")
		}

		if err := deps.ReflexEngine.Delete(name); err != nil {
			return "", fmt.Errorf("failed to delete reflex: %w", err)
		}

		log.Printf("Deleted reflex: %s", name)
		return fmt.Sprintf("Reflex deleted: %s", name), nil
	})
}

func registerCalendarTools(server *mcp.Server, deps *Dependencies) {
	// calendar_today
	server.RegisterTool("calendar_today", mcp.ToolDef{
		Description: "Get today's calendar events. Returns compact format by default (one line per event).",
		Properties: map[string]mcp.PropDef{
			"verbose": {Type: "boolean", Description: "If true, return full JSON with all event details. Default: false (compact format)"},
		},
	}, func(ctx any, args map[string]any) (string, error) {
		events, err := deps.CalendarClient.GetTodayEvents(context.Background())
		if err != nil {
			return "", fmt.Errorf("failed to get today's events: %w", err)
		}

		if len(events) == 0 {
			return "No events scheduled for today.", nil
		}

		if verbose, _ := args["verbose"].(bool); verbose {
			data, _ := json.MarshalIndent(events, "", "  ")
			return string(data), nil
		}

		return formatEventsCompact(events), nil
	})

	// calendar_upcoming
	server.RegisterTool("calendar_upcoming", mcp.ToolDef{
		Description: "Get upcoming calendar events within a time window. Returns compact format by default.",
		Properties: map[string]mcp.PropDef{
			"duration":    {Type: "string", Description: "Time window to look ahead (e.g., '24h', '7d'). Default: 24h"},
			"max_results": {Type: "number", Description: "Maximum number of events to return. Default: 20"},
			"verbose":     {Type: "boolean", Description: "If true, return full JSON with all event details. Default: false (compact format)"},
		},
	}, func(ctx any, args map[string]any) (string, error) {
		durationStr, _ := args["duration"].(string)
		if durationStr == "" {
			durationStr = "24h"
		}

		duration, err := time.ParseDuration(durationStr)
		if err != nil {
			return "", fmt.Errorf("invalid duration: %w", err)
		}

		maxResults := 20
		if n, ok := args["max_results"].(float64); ok && n > 0 {
			maxResults = int(n)
		}

		events, err := deps.CalendarClient.GetUpcomingEvents(context.Background(), duration, maxResults)
		if err != nil {
			return "", fmt.Errorf("failed to get upcoming events: %w", err)
		}

		if len(events) == 0 {
			return fmt.Sprintf("No events in the next %s.", durationStr), nil
		}

		if verbose, _ := args["verbose"].(bool); verbose {
			data, _ := json.MarshalIndent(events, "", "  ")
			return string(data), nil
		}

		return formatEventsCompact(events), nil
	})

	// calendar_list_events - query events in a date range
	server.RegisterTool("calendar_list_events", mcp.ToolDef{
		Description: "Query calendar events in a specific date range. Returns compact format by default.",
		Properties: map[string]mcp.PropDef{
			"time_min":    {Type: "string", Description: "Start of time range (RFC3339 or YYYY-MM-DD). Default: now"},
			"time_max":    {Type: "string", Description: "End of time range (RFC3339 or YYYY-MM-DD). Default: 1 week from time_min"},
			"max_results": {Type: "number", Description: "Maximum number of events to return. Default: 50"},
			"query":       {Type: "string", Description: "Text to search for in event titles/descriptions (optional)"},
			"verbose":     {Type: "boolean", Description: "If true, return full JSON with all event details. Default: false (compact format)"},
		},
	}, func(ctx any, args map[string]any) (string, error) {
		timeMinStr, _ := args["time_min"].(string)
		timeMaxStr, _ := args["time_max"].(string)

		var timeMin, timeMax time.Time
		var err error

		if timeMinStr == "" {
			timeMin = time.Now()
		} else {
			timeMin, err = time.Parse(time.RFC3339, timeMinStr)
			if err != nil {
				timeMin, err = time.Parse("2006-01-02", timeMinStr)
				if err != nil {
					return "", fmt.Errorf("invalid time_min format (use RFC3339 or YYYY-MM-DD): %w", err)
				}
			}
		}

		if timeMaxStr == "" {
			timeMax = timeMin.Add(7 * 24 * time.Hour)
		} else {
			timeMax, err = time.Parse(time.RFC3339, timeMaxStr)
			if err != nil {
				timeMax, err = time.Parse("2006-01-02", timeMaxStr)
				if err != nil {
					return "", fmt.Errorf("invalid time_max format (use RFC3339 or YYYY-MM-DD): %w", err)
				}
				timeMax = timeMax.Add(24 * time.Hour)
			}
		}

		params := calendar.ListEventsParams{
			TimeMin:    timeMin,
			TimeMax:    timeMax,
			MaxResults: 50,
		}

		if n, ok := args["max_results"].(float64); ok && n > 0 {
			params.MaxResults = int(n)
		}
		if query, ok := args["query"].(string); ok {
			params.Query = query
		}

		events, err := deps.CalendarClient.ListEvents(context.Background(), params)
		if err != nil {
			return "", fmt.Errorf("failed to list events: %w", err)
		}

		if len(events) == 0 {
			return "No events found in the specified time range.", nil
		}

		if verbose, _ := args["verbose"].(bool); verbose {
			data, _ := json.MarshalIndent(events, "", "  ")
			return string(data), nil
		}

		return formatEventsCompact(events), nil
	})

	// calendar_free_busy - check availability
	server.RegisterTool("calendar_free_busy", mcp.ToolDef{
		Description: "Check calendar availability/free-busy status for a time range.",
		Properties: map[string]mcp.PropDef{
			"time_min": {Type: "string", Description: "Start of time range (RFC3339 or YYYY-MM-DD). Default: now"},
			"time_max": {Type: "string", Description: "End of time range (RFC3339 or YYYY-MM-DD). Default: 24h from time_min"},
		},
	}, func(ctx any, args map[string]any) (string, error) {
		timeMinStr, _ := args["time_min"].(string)
		timeMaxStr, _ := args["time_max"].(string)

		var timeMin, timeMax time.Time
		var err error

		if timeMinStr == "" {
			timeMin = time.Now()
		} else {
			timeMin, err = time.Parse(time.RFC3339, timeMinStr)
			if err != nil {
				timeMin, err = time.Parse("2006-01-02", timeMinStr)
				if err != nil {
					return "", fmt.Errorf("invalid time_min format: %w", err)
				}
			}
		}

		if timeMaxStr == "" {
			timeMax = timeMin.Add(24 * time.Hour)
		} else {
			timeMax, err = time.Parse(time.RFC3339, timeMaxStr)
			if err != nil {
				timeMax, err = time.Parse("2006-01-02", timeMaxStr)
				if err != nil {
					return "", fmt.Errorf("invalid time_max format: %w", err)
				}
				timeMax = timeMax.Add(24 * time.Hour)
			}
		}

		busy, err := deps.CalendarClient.FreeBusy(context.Background(), calendar.FreeBusyParams{
			TimeMin: timeMin,
			TimeMax: timeMax,
		})
		if err != nil {
			return "", fmt.Errorf("failed to get free/busy: %w", err)
		}

		if len(busy) == 0 {
			return fmt.Sprintf("Free from %s to %s", timeMin.Format("Jan 2 15:04"), timeMax.Format("Jan 2 15:04")), nil
		}

		var lines []string
		lines = append(lines, fmt.Sprintf("Busy periods from %s to %s:", timeMin.Format("Jan 2 15:04"), timeMax.Format("Jan 2 15:04")))
		for _, b := range busy {
			lines = append(lines, fmt.Sprintf("  %s - %s", b.Start.Format("Jan 2 15:04"), b.End.Format("15:04")))
		}
		return strings.Join(lines, "\n"), nil
	})

	// calendar_get_event - get a specific event by ID
	server.RegisterTool("calendar_get_event", mcp.ToolDef{
		Description: "Get details of a specific calendar event by ID.",
		Properties: map[string]mcp.PropDef{
			"event_id": {Type: "string", Description: "The event ID to retrieve"},
		},
		Required: []string{"event_id"},
	}, func(ctx any, args map[string]any) (string, error) {
		eventID, _ := args["event_id"].(string)
		if eventID == "" {
			return "", fmt.Errorf("event_id is required")
		}

		event, err := deps.CalendarClient.GetEvent(context.Background(), eventID)
		if err != nil {
			return "", fmt.Errorf("failed to get event: %w", err)
		}

		data, _ := json.MarshalIndent(event, "", "  ")
		return string(data), nil
	})

	// calendar_create_event - create a new event
	server.RegisterTool("calendar_create_event", mcp.ToolDef{
		Description: "Create a new calendar event.",
		Properties: map[string]mcp.PropDef{
			"summary":     {Type: "string", Description: "Event title/summary"},
			"start":       {Type: "string", Description: "Start time (RFC3339 or YYYY-MM-DD for all-day events)"},
			"end":         {Type: "string", Description: "End time (RFC3339 or YYYY-MM-DD). Default: 1 hour after start"},
			"description": {Type: "string", Description: "Event description (optional)"},
			"location":    {Type: "string", Description: "Event location (optional)"},
			"attendees":   {Type: "array", Description: "List of attendee email addresses (optional)"},
		},
		Required: []string{"summary", "start"},
	}, func(ctx any, args map[string]any) (string, error) {
		summary, _ := args["summary"].(string)
		if summary == "" {
			return "", fmt.Errorf("summary is required")
		}

		startStr, _ := args["start"].(string)
		endStr, _ := args["end"].(string)
		if startStr == "" {
			return "", fmt.Errorf("start time is required")
		}

		var start, end time.Time
		var err error
		var allDay bool

		start, err = time.Parse(time.RFC3339, startStr)
		if err != nil {
			start, err = time.Parse("2006-01-02", startStr)
			if err != nil {
				return "", fmt.Errorf("invalid start format (use RFC3339 or YYYY-MM-DD): %w", err)
			}
			allDay = true
		}

		if endStr == "" {
			if allDay {
				end = start.Add(24 * time.Hour)
			} else {
				end = start.Add(time.Hour)
			}
		} else {
			end, err = time.Parse(time.RFC3339, endStr)
			if err != nil {
				end, err = time.Parse("2006-01-02", endStr)
				if err != nil {
					return "", fmt.Errorf("invalid end format: %w", err)
				}
			}
		}

		params := calendar.CreateEventParams{
			Summary: summary,
			Start:   start,
			End:     end,
			AllDay:  allDay,
		}

		if desc, ok := args["description"].(string); ok {
			params.Description = desc
		}
		if loc, ok := args["location"].(string); ok {
			params.Location = loc
		}
		if attendees, ok := args["attendees"].([]any); ok {
			for _, a := range attendees {
				if email, ok := a.(string); ok {
					params.Attendees = append(params.Attendees, email)
				}
			}
		}

		event, err := deps.CalendarClient.CreateEvent(context.Background(), params)
		if err != nil {
			return "", fmt.Errorf("failed to create event: %w", err)
		}

		log.Printf("Created calendar event: %s", summary)
		data, _ := json.MarshalIndent(event, "", "  ")
		return string(data), nil
	})
}

func registerGitHubTools(server *mcp.Server, deps *Dependencies) {
	// github_list_projects
	server.RegisterTool("github_list_projects", mcp.ToolDef{
		Description: "List all GitHub Projects v2 in the configured organization. Returns project number, title, and URL.",
		Properties:  map[string]mcp.PropDef{},
	}, func(ctx any, args map[string]any) (string, error) {
		projects, err := deps.GitHubClient.ListProjects()
		if err != nil {
			return "", fmt.Errorf("failed to list projects: %w", err)
		}

		data, _ := json.MarshalIndent(projects, "", "  ")
		return string(data), nil
	})

	// github_get_project
	server.RegisterTool("github_get_project", mcp.ToolDef{
		Description: "Get a GitHub Project by number. Returns project details and field schema.",
		Properties: map[string]mcp.PropDef{
			"number": {Type: "number", Description: "The project number (visible in project URL)"},
		},
		Required: []string{"number"},
	}, func(ctx any, args map[string]any) (string, error) {
		number, ok := args["number"].(float64)
		if !ok || number == 0 {
			return "", fmt.Errorf("number is required")
		}

		project, err := deps.GitHubClient.GetProject(int(number))
		if err != nil {
			return "", fmt.Errorf("failed to get project: %w", err)
		}

		fields, err := deps.GitHubClient.GetProjectFields(int(number))
		if err != nil {
			return "", fmt.Errorf("failed to get project fields: %w", err)
		}

		result := map[string]any{
			"number": project.Number,
			"title":  project.Title,
			"url":    project.URL,
			"closed": project.Closed,
			"fields": fields,
		}

		data, _ := json.MarshalIndent(result, "", "  ")
		return string(data), nil
	})

	// github_project_items
	server.RegisterTool("github_project_items", mcp.ToolDef{
		Description: "Query items from a GitHub Project. Returns compact list format by default. Use status filter for views like backlog/sprint.",
		Properties: map[string]mcp.PropDef{
			"project":   {Type: "number", Description: "The project number"},
			"status":    {Type: "string", Description: "Filter by Status field (e.g., 'Backlog', 'In Progress', 'Done')"},
			"sprint":    {Type: "string", Description: "Filter by Sprint (e.g., 'Sprint 65'). Use 'backlog' for items with no sprint assigned."},
			"priority":  {Type: "string", Description: "Filter by Priority field (e.g., 'P0', 'P1', 'P2')"},
			"team_area": {Type: "string", Description: "Filter by Team / Area field (e.g., 'SE', 'Docs', 'Nexus', 'DevOps')"},
			"max_items": {Type: "number", Description: "Maximum items to return (default 100)"},
			"verbose":   {Type: "boolean", Description: "If true, return full JSON with all fields. Default: false (compact format)"},
		},
		Required: []string{"project"},
	}, func(ctx any, args map[string]any) (string, error) {
		projectNum, ok := args["project"].(float64)
		if !ok {
			return "", fmt.Errorf("project number is required")
		}

		params := github.QueryItemsParams{
			ProjectNumber: int(projectNum),
		}
		if status, ok := args["status"].(string); ok {
			params.Status = status
		}
		if sprint, ok := args["sprint"].(string); ok {
			params.Sprint = sprint
		}
		if priority, ok := args["priority"].(string); ok {
			params.Priority = priority
		}
		if teamArea, ok := args["team_area"].(string); ok {
			params.TeamArea = teamArea
		}
		if n, ok := args["max_items"].(float64); ok && n > 0 {
			params.MaxItems = int(n)
		}

		items, err := deps.GitHubClient.QueryItems(params)
		if err != nil {
			return "", fmt.Errorf("failed to get project items: %w", err)
		}

		data, _ := json.MarshalIndent(items, "", "  ")
		return string(data), nil
	})
}

func registerEvalTools(server *mcp.Server, deps *Dependencies) {
	activityPath := filepath.Join(deps.SystemPath, "activity.jsonl")

	// memory_judge_sample
	server.RegisterTool("memory_judge_sample", mcp.ToolDef{
		Description: "Run independent evaluation of recent memory retrievals. Compares LLM judge ratings against self-eval to detect bias.",
		Properties: map[string]mcp.PropDef{
			"sample_size": {Type: "number", Description: "Number of memories to evaluate (default 20, max 50)"},
		},
	}, func(ctx any, args map[string]any) (string, error) {
		sampleSize := 20
		if n, ok := args["sample_size"].(float64); ok && n > 0 {
			sampleSize = int(n)
			if sampleSize > 50 {
				sampleSize = 50
			}
		}

		report, err := deps.MemoryJudge.EvaluateSample(activityPath, sampleSize)
		if err != nil {
			return "", fmt.Errorf("evaluation failed: %w", err)
		}

		summary := fmt.Sprintf(`Memory Judge Report
==================
Sample size: %d
Self-eval avg: %.2f
Judge avg: %.2f
Bias (judge - self): %.2f
Correlation: %.2f
Agreement (±1 point): %.0f%%

Outliers (|diff| >= 2): %d`,
			report.SampleSize,
			report.SelfAvg,
			report.JudgeAvg,
			report.Bias,
			report.Correlation,
			report.Agreement*100,
			len(report.Outliers))

		if len(report.Outliers) > 0 {
			summary += "\n\nOutlier details:"
			for _, o := range report.Outliers {
				summary += fmt.Sprintf("\n- Self=%d Judge=%d: %s", o.SelfRating, o.JudgeRating, o.Memory)
			}
		}

		log.Printf("Memory judge: evaluated %d samples, bias=%.2f, correlation=%.2f", report.SampleSize, report.Bias, report.Correlation)
		return summary, nil
	})
}

// Helper functions

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func formatEventsCompact(events []calendar.Event) string {
	var lines []string
	for _, e := range events {
		line := fmt.Sprintf("%s - %s: %s", e.Start.Format("15:04"), e.End.Format("15:04"), e.Summary)
		if e.Location != "" {
			line += fmt.Sprintf(" @ %s", e.Location)
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}
