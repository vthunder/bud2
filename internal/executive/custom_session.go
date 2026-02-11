package executive

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os/exec"
	"strings"
	"sync"
)

// CustomSessionConfig holds configuration for custom Claude sessions
// These are non-Bud sessions for specific tasks like relationship inference,
// code analysis, summarization, etc. They skip all Bud identity and tools.
type CustomSessionConfig struct {
	// Model to use (e.g., "claude-sonnet-4-5")
	Model string
	// Working directory for Claude
	WorkDir string
	// Verbose logging (default: false)
	Verbose bool
}

// CustomSessionResult holds the result from a custom session
type CustomSessionResult struct {
	// Text output from Claude
	Output string
	// Token usage metrics
	Usage *SessionUsage
	// Any errors encountered
	Error error
}

// RunCustomSession executes a one-shot Claude session with a custom prompt.
// This is a simplified, stateless wrapper around `claude --print` for non-Bud tasks.
//
// Unlike SimpleSession (which manages Bud's full conversational state), this:
// - No session tracking or resumption
// - No Bud identity, memories, or MCP tools
// - Just sends prompt, returns response
// - Perfect for LLM-powered utilities (inference, analysis, etc.)
func RunCustomSession(ctx context.Context, prompt string, cfg CustomSessionConfig) *CustomSessionResult {
	result := &CustomSessionResult{}

	args := []string{
		"--print",
		"--dangerously-skip-permissions",
		"--output-format", "stream-json",
		"--verbose", // Required by claude CLI when using --print with stream-json
	}

	// Always create fresh session (one-shot mode)
	sessionID := generateSessionUUID()
	args = append(args, "--session-id", sessionID)

	if cfg.Model != "" {
		args = append(args, "--model", cfg.Model)
	}

	// Add prompt as positional argument (claude expects it as arg, not stdin)
	args = append(args, prompt)

	cmd := exec.CommandContext(ctx, "claude", args...)
	if cfg.WorkDir != "" {
		cmd.Dir = cfg.WorkDir
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		result.Error = fmt.Errorf("failed to get stdout pipe: %w", err)
		return result
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		result.Error = fmt.Errorf("failed to get stderr pipe: %w", err)
		return result
	}

	if cfg.Verbose {
		log.Printf("[custom-session] Starting with prompt: %s", truncatePrompt(prompt, 100))
	}

	if err := cmd.Start(); err != nil {
		result.Error = fmt.Errorf("failed to start claude: %w", err)
		return result
	}

	var wg sync.WaitGroup
	var stderrBuf strings.Builder
	var outputBuf strings.Builder
	var usage *SessionUsage

	wg.Add(2)

	// Process stdout (stream-json events)
	go func() {
		defer wg.Done()
		usage = processCustomStreamJSON(stdout, &outputBuf, cfg.Verbose)
	}()

	// Process stderr
	go func() {
		defer wg.Done()
		processCustomStderr(stderr, &stderrBuf, cfg.Verbose)
	}()

	wg.Wait()

	if err := cmd.Wait(); err != nil {
		if stderrBuf.Len() > 0 {
			result.Error = fmt.Errorf("claude exited with error: %w\nstderr: %s", err, stderrBuf.String())
		} else {
			result.Error = fmt.Errorf("claude exited with error: %w", err)
		}
		return result
	}

	result.Output = outputBuf.String()
	result.Usage = usage

	if cfg.Verbose && usage != nil {
		log.Printf("[custom-session] Completion: input=%d output=%d cache_read=%d duration=%dms",
			usage.InputTokens, usage.OutputTokens, usage.CacheReadInputTokens, usage.DurationMs)
	}

	return result
}

// processCustomStreamJSON parses Claude's stream-json output and accumulates text
func processCustomStreamJSON(r io.Reader, output *strings.Builder, verbose bool) *SessionUsage {
	scanner := bufio.NewScanner(r)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	var usage *SessionUsage

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		var event StreamEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			if verbose {
				log.Printf("[custom-session] Failed to parse event: %v", err)
			}
			continue
		}

		switch event.Type {
		case "result":
			// Extract text output
			if event.Result != nil {
				var result string
				if err := json.Unmarshal(event.Result, &result); err == nil && result != "" {
					output.WriteString(result)
				}
			}
			// Extract usage
			usage = parseCustomResultUsage([]byte(line), verbose)

		case "content_block_delta":
			// Streaming text deltas
			var delta struct {
				Delta struct {
					Text string `json:"text"`
				} `json:"delta"`
			}
			if err := json.Unmarshal(event.Content, &delta); err == nil {
				if delta.Delta.Text != "" {
					output.WriteString(delta.Delta.Text)
				}
			}
		}
	}

	if err := scanner.Err(); err != nil && verbose {
		log.Printf("[custom-session] Scanner error: %v", err)
	}

	return usage
}

// parseCustomResultUsage extracts token usage from a raw result event JSON line
func parseCustomResultUsage(raw []byte, verbose bool) *SessionUsage {
	var result struct {
		NumTurns      int `json:"num_turns"`
		DurationMs    int `json:"duration_ms"`
		DurationApiMs int `json:"duration_api_ms"`
		Usage         struct {
			InputTokens              int `json:"input_tokens"`
			OutputTokens             int `json:"output_tokens"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens"`
		} `json:"usage"`
		ModelUsage map[string]struct {
			ContextWindow   int `json:"contextWindow"`
			MaxOutputTokens int `json:"maxOutputTokens"`
		} `json:"modelUsage"`
	}

	if err := json.Unmarshal(raw, &result); err != nil {
		if verbose {
			log.Printf("[custom-session] Failed to parse result usage: %v", err)
		}
		return nil
	}

	usage := &SessionUsage{
		InputTokens:              result.Usage.InputTokens,
		OutputTokens:             result.Usage.OutputTokens,
		CacheCreationInputTokens: result.Usage.CacheCreationInputTokens,
		CacheReadInputTokens:     result.Usage.CacheReadInputTokens,
		NumTurns:                 result.NumTurns,
		DurationMs:               result.DurationMs,
		DurationApiMs:            result.DurationApiMs,
	}

	// Extract context window from first model in modelUsage
	for _, m := range result.ModelUsage {
		usage.ContextWindow = m.ContextWindow
		usage.MaxOutputTokens = m.MaxOutputTokens
		break
	}

	return usage
}

// processCustomStderr captures stderr output
func processCustomStderr(r io.Reader, buf *strings.Builder, verbose bool) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		if line != "" {
			if verbose {
				log.Printf("[custom-session stderr] %s", line)
			}
			if buf != nil {
				buf.WriteString(line + "\n")
			}
		}
	}
}

// Removed: truncatePrompt is defined in simple_session.go
