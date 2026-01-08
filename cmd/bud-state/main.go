package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/vthunder/bud2/internal/state"
	"github.com/vthunder/bud2/internal/types"
)

func main() {
	// Global flags
	statePath := os.Getenv("BUD_STATE_PATH")
	if statePath == "" {
		statePath = "state"
	}

	if len(os.Args) < 2 {
		printUsage()
		os.Exit(0)
	}

	inspector := state.NewInspector(statePath)
	cmd := os.Args[1]

	switch cmd {
	case "summary", "":
		handleSummary(inspector)
	case "health":
		handleHealth(inspector)
	case "traces":
		handleTraces(inspector, statePath, os.Args[2:])
	case "percepts":
		handlePercepts(inspector, os.Args[2:])
	case "threads":
		handleThreads(inspector, os.Args[2:])
	case "logs":
		handleLogs(inspector, os.Args[2:])
	case "queues":
		handleQueues(inspector, os.Args[2:])
	case "sessions":
		handleSessions(inspector, os.Args[2:])
	case "help", "-h", "--help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", cmd)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println(`bud-state - Inspect and manage Bud's internal state

Usage: bud-state <command> [options]

Commands:
  summary              Overview of all state components (default)
  health               Run health checks with recommendations

  traces               List all traces
  traces <id>          Show full trace
  traces -d <id>       Delete specific trace
  traces --clear       Clear all non-core traces
  traces --clear-core  Clear core traces (will need regeneration)
  traces --regen-core  Regenerate core traces from core_seed.md

  percepts             List all percepts
  percepts --count     Just show count
  percepts --clear     Clear all percepts
  percepts --clear --older-than=1h  Clear percepts older than duration

  threads              List all threads
  threads <id>         Show full thread
  threads --clear      Clear all threads
  threads --clear --status=frozen  Clear threads by status

  logs                 Tail recent journal + activity entries
  logs --truncate=100  Keep only last N entries in each log

  queues               Show inbox/outbox/signals counts
  queues --clear       Clear all queues

  sessions             List sessions
  sessions --clear     Clear session tracking

Environment:
  BUD_STATE_PATH       State directory (default: "state")`)
}

func handleSummary(inspector *state.Inspector) {
	summary, err := inspector.Summary()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("State Summary")
	fmt.Println("=============")
	fmt.Printf("Traces:    %d total, %d core\n", summary.Traces.Total, summary.Traces.Core)
	fmt.Printf("Percepts:  %d\n", summary.Percepts.Total)
	fmt.Printf("Threads:   %d\n", summary.Threads.Total)
	fmt.Printf("Journal:   %d entries\n", summary.Journal)
	fmt.Printf("Activity:  %d entries\n", summary.Activity)
	fmt.Printf("Inbox:     %d\n", summary.Inbox)
	fmt.Printf("Outbox:    %d\n", summary.Outbox)
	fmt.Printf("Signals:   %d\n", summary.Signals)
}

func handleHealth(inspector *state.Inspector) {
	health, err := inspector.Health()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Health Status: %s\n", health.Status)
	if len(health.Warnings) > 0 {
		fmt.Println("\nWarnings:")
		for _, w := range health.Warnings {
			fmt.Printf("  - %s\n", w)
		}
	}
	if len(health.Recommendations) > 0 {
		fmt.Println("\nRecommendations:")
		for _, r := range health.Recommendations {
			fmt.Printf("  - %s\n", r)
		}
	}
}

func handleTraces(inspector *state.Inspector, statePath string, args []string) {
	fs := flag.NewFlagSet("traces", flag.ExitOnError)
	deleteID := fs.String("d", "", "Delete trace by ID")
	clear := fs.Bool("clear", false, "Clear all non-core traces")
	clearCore := fs.Bool("clear-core", false, "Clear core traces")
	regenCore := fs.Bool("regen-core", false, "Regenerate core from seed")
	fs.Parse(args)

	if *regenCore {
		seedPath := filepath.Join(statePath, "core_seed.md")
		count, err := inspector.RegenCore(seedPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Regenerated %d core traces from %s\n", count, seedPath)
		return
	}

	if *clearCore {
		count, err := inspector.ClearTraces(true)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Cleared %d core traces\n", count)
		return
	}

	if *clear {
		count, err := inspector.ClearTraces(false)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Cleared %d non-core traces\n", count)
		return
	}

	if *deleteID != "" {
		if err := inspector.DeleteTrace(*deleteID); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Deleted trace: %s\n", *deleteID)
		return
	}

	// Show single trace or list all
	if fs.NArg() > 0 {
		trace, err := inspector.GetTrace(fs.Arg(0))
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		data, _ := json.MarshalIndent(trace, "", "  ")
		fmt.Println(string(data))
		return
	}

	// List all
	traces, err := inspector.ListTraces()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Traces (%d total)\n", len(traces))
	fmt.Println("================")
	for _, t := range traces {
		coreMarker := ""
		if t.IsCore {
			coreMarker = " [CORE]"
		}
		fmt.Printf("%s%s (strength=%d)\n  %s\n\n", t.ID, coreMarker, t.Strength, t.Content)
	}
}

func handlePercepts(inspector *state.Inspector, args []string) {
	fs := flag.NewFlagSet("percepts", flag.ExitOnError)
	countOnly := fs.Bool("count", false, "Just show count")
	clear := fs.Bool("clear", false, "Clear percepts")
	olderThan := fs.String("older-than", "", "Clear percepts older than duration (e.g., 1h, 30m)")
	fs.Parse(args)

	if *clear {
		var dur time.Duration
		if *olderThan != "" {
			var err error
			dur, err = time.ParseDuration(*olderThan)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Invalid duration: %v\n", err)
				os.Exit(1)
			}
		}
		count, err := inspector.ClearPercepts(dur)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		if dur > 0 {
			fmt.Printf("Cleared %d percepts older than %s\n", count, dur)
		} else {
			fmt.Println("Cleared all percepts")
		}
		return
	}

	percepts, err := inspector.ListPercepts()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if *countOnly {
		fmt.Printf("%d\n", len(percepts))
		return
	}

	fmt.Printf("Percepts (%d total)\n", len(percepts))
	fmt.Println("==================")
	for _, p := range percepts {
		age := time.Since(p.Timestamp).Round(time.Second)
		fmt.Printf("%s (%s, %s ago)\n  %s\n\n", p.ID, p.Source, age, p.Preview)
	}
}

func handleThreads(inspector *state.Inspector, args []string) {
	fs := flag.NewFlagSet("threads", flag.ExitOnError)
	clear := fs.Bool("clear", false, "Clear threads")
	status := fs.String("status", "", "Filter by status (active, paused, frozen, complete)")
	fs.Parse(args)

	if *clear {
		var statusPtr *types.ThreadStatus
		if *status != "" {
			s := types.ThreadStatus(*status)
			statusPtr = &s
		}
		count, err := inspector.ClearThreads(statusPtr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		if statusPtr != nil {
			fmt.Printf("Cleared %d threads with status=%s\n", count, *status)
		} else {
			fmt.Println("Cleared all threads")
		}
		return
	}

	// Show single thread or list all
	if fs.NArg() > 0 {
		thread, err := inspector.GetThread(fs.Arg(0))
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		data, _ := json.MarshalIndent(thread, "", "  ")
		fmt.Println(string(data))
		return
	}

	threads, err := inspector.ListThreads()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Threads (%d total)\n", len(threads))
	fmt.Println("=================")
	for _, t := range threads {
		fmt.Printf("%s (status=%s, session=%s, %d percepts)\n",
			t.ID, t.Status, t.SessionState, t.PerceptCount)
	}
}

func handleLogs(inspector *state.Inspector, args []string) {
	fs := flag.NewFlagSet("logs", flag.ExitOnError)
	truncate := fs.Int("truncate", 0, "Keep only last N entries")
	count := fs.Int("n", 20, "Number of entries to show")
	fs.Parse(args)

	if *truncate > 0 {
		if err := inspector.TruncateLogs(*truncate); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Truncated logs to last %d entries\n", *truncate)
		return
	}

	entries, err := inspector.TailLogs(*count)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Recent Log Entries (%d)\n", len(entries))
	fmt.Println("======================")
	for _, e := range entries {
		source := e["_source"]
		delete(e, "_source")
		ts := ""
		if t, ok := e["timestamp"].(string); ok {
			ts = t
		}
		summary := ""
		if s, ok := e["summary"].(string); ok {
			summary = s
		}
		fmt.Printf("[%s] %s: %s\n", source, ts, summary)
	}
}

func handleQueues(inspector *state.Inspector, args []string) {
	fs := flag.NewFlagSet("queues", flag.ExitOnError)
	clear := fs.Bool("clear", false, "Clear all queues")
	fs.Parse(args)

	if *clear {
		if err := inspector.ClearQueues(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Cleared all queues")
		return
	}

	queues, err := inspector.ListQueues()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Queues")
	fmt.Println("======")
	fmt.Printf("Inbox:   %d\n", queues.Inbox)
	fmt.Printf("Outbox:  %d\n", queues.Outbox)
	fmt.Printf("Signals: %d\n", queues.Signals)
}

func handleSessions(inspector *state.Inspector, args []string) {
	fs := flag.NewFlagSet("sessions", flag.ExitOnError)
	clear := fs.Bool("clear", false, "Clear session tracking")
	fs.Parse(args)

	if *clear {
		if err := inspector.ClearSessions(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Cleared sessions")
		return
	}

	sessions, err := inspector.ListSessions()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Sessions (%d)\n", len(sessions))
	fmt.Println("============")
	for _, s := range sessions {
		fmt.Printf("%s (status=%s)\n", s.ID, s.Status)
	}
}
