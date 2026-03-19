// cmd/sdk-harness/main.go
//
// Empirical test harness for SDK/session design investigation.
// Tests key questions before committing to either long-lived sessions or subagent patterns.
//
// Run: go run ./cmd/sdk-harness/
// Or build: go build -o bin/sdk-harness ./cmd/sdk-harness/

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"
)

func main() {
	fmt.Println("=== SDK Session Design Harness ===")
	fmt.Println()

	results := []TestResult{}

	tests := []struct {
		name string
		fn   func() TestResult
	}{
		{"T1: AskUserQuestion in --print mode", testAskUserQuestion},
		{"T2: PreToolUse hook intercept", testPreToolUseHook},
		{"T3: Session resume (multi-turn)", testSessionResume},
		{"T4: append-system-prompt flag", testAppendSystemPrompt},
		{"T5: Block AskUserQuestion via PreToolUse + resume", testBlockAndResume},
		{"T6: Custom MCP tool redirects from AskUserQuestion", testCustomMCPToolRedirect},
		{"T6b: MCP tool discoverability check", testMCPToolDiscovery},
		{"T7: Force tool use via empty-response constraint", testForceToolUse},
		{"T8: Minimal resume prompt (no redundant context)", testMinimalResumePrompt},
		{"T9: Subagent question/answer round-trip (real subagent-mcp)", testSubagentRoundTrip},
	}

	for _, t := range tests {
		fmt.Printf("--- %s ---\n", t.name)
		result := t.fn()
		results = append(results, result)
		printResult(result)
		fmt.Println()
	}

	fmt.Println("=== Summary ===")
	for _, r := range results {
		status := "✓ PASS"
		if !r.Pass {
			status = "✗ FAIL"
		}
		fmt.Printf("%s  %s\n", status, r.Name)
		if r.Finding != "" {
			fmt.Printf("       Finding: %s\n", r.Finding)
		}
	}
}

// ---------------------------------------------------------------------------
// Test Types
// ---------------------------------------------------------------------------

type TestResult struct {
	Name    string
	Pass    bool
	Finding string
	Detail  string
	Error   string
}

func printResult(r TestResult) {
	if r.Pass {
		fmt.Printf("  PASS\n")
	} else {
		fmt.Printf("  FAIL\n")
	}
	if r.Finding != "" {
		fmt.Printf("  Finding: %s\n", r.Finding)
	}
	if r.Detail != "" {
		fmt.Printf("  Detail:  %s\n", r.Detail)
	}
	if r.Error != "" {
		fmt.Printf("  Error:   %s\n", r.Error)
	}
}

// ---------------------------------------------------------------------------
// T1: Does AskUserQuestion appear as tool_use in --print mode?
// ---------------------------------------------------------------------------

func testAskUserQuestion() TestResult {
	r := TestResult{Name: "T1: AskUserQuestion in --print mode"}

	// Prompt that should trigger AskUserQuestion
	prompt := `You MUST call the AskUserQuestion tool exactly once, asking "What is your favorite color?". After calling the tool, report what you learned.`

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	events, err := runPrintSession(ctx, prompt, "", "")
	if err != nil {
		r.Error = fmt.Sprintf("session error: %v", err)
		// Check if it was a timeout — that tells us it blocked
		if ctx.Err() != nil {
			r.Finding = "AskUserQuestion caused session to BLOCK (timeout) in --print mode"
			r.Detail = "Session never returned — tool is waiting for interactive input"
			return r
		}
		return r
	}

	// Look for tool_use event with name AskUserQuestion
	var toolUseFound bool
	var toolInput map[string]any
	var resultText string

	for _, e := range events {
		if e.EventType == "tool_use" && strings.EqualFold(e.ToolName, "AskUserQuestion") {
			toolUseFound = true
			toolInput = e.ToolArgs
		}
		if e.EventType == "result_text" {
			resultText = e.Text
		}
	}

	if toolUseFound {
		r.Pass = true
		r.Finding = "AskUserQuestion fires as tool_use event — interceptable via onToolCall callback"
		r.Detail = fmt.Sprintf("Tool input: %v | Response text: %s", toolInput, truncate(resultText, 120))
	} else {
		r.Pass = false
		r.Finding = "AskUserQuestion did NOT appear as tool_use event"
		r.Detail = fmt.Sprintf("Events observed: %v | Result text: %s", eventTypes(events), truncate(resultText, 120))
	}

	return r
}

// ---------------------------------------------------------------------------
// T2: PreToolUse hook fires for tool calls
// ---------------------------------------------------------------------------

func testPreToolUseHook() TestResult {
	r := TestResult{Name: "T2: PreToolUse hook intercept"}

	// Write a hook script that logs tool_name to a temp file
	logFile := "/tmp/sdk-harness-hook-log.txt"
	os.Remove(logFile)

	hookScript := fmt.Sprintf(`#!/bin/bash
INPUT=$(cat)
TOOL=$(echo "$INPUT" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('tool_name','unknown'))" 2>/dev/null || echo "parse_error")
echo "$TOOL" >> %s
# Return empty = approve
exit 0
`, logFile)

	hookScriptPath := "/tmp/sdk-harness-hook.sh"
	if err := os.WriteFile(hookScriptPath, []byte(hookScript), 0755); err != nil {
		r.Error = fmt.Sprintf("failed to write hook script: %v", err)
		return r
	}

	// Build settings JSON with PreToolUse hook
	settingsJSON := fmt.Sprintf(`{
  "hooks": {
    "PreToolUse": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "%s"
          }
        ]
      }
    ]
  }
}`, hookScriptPath)

	settingsFile := "/tmp/sdk-harness-settings.json"
	if err := os.WriteFile(settingsFile, []byte(settingsJSON), 0644); err != nil {
		r.Error = fmt.Sprintf("failed to write settings file: %v", err)
		return r
	}

	// Run a session that will use a built-in tool (Bash is reliable)
	prompt := `Run this bash command: echo hello_from_harness`

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	events, err := runPrintSession(ctx, prompt, settingsFile, "--tools=Bash")
	if err != nil {
		r.Error = fmt.Sprintf("session error: %v", err)
		return r
	}

	// Check if hook log file was written
	logData, err := os.ReadFile(logFile)
	if err != nil {
		r.Pass = false
		r.Finding = "Hook did NOT fire — log file was not created"
		r.Detail = fmt.Sprintf("Events: %v", eventTypes(events))
		return r
	}

	logContents := strings.TrimSpace(string(logData))
	if logContents != "" {
		r.Pass = true
		r.Finding = fmt.Sprintf("PreToolUse hook fires — tool names logged: %s", logContents)
		r.Detail = fmt.Sprintf("Events: %v", eventTypes(events))
	} else {
		r.Pass = false
		r.Finding = "Hook fired but log was empty"
		r.Detail = fmt.Sprintf("Events: %v", eventTypes(events))
	}

	return r
}

// ---------------------------------------------------------------------------
// T3: Session resume — does --resume work in --print mode?
// ---------------------------------------------------------------------------

func testSessionResume() TestResult {
	r := TestResult{Name: "T3: Session resume (multi-turn)"}

	// Session 1: store a value
	prompt1 := `Remember the number 7777. Say exactly: "Stored 7777"`

	ctx1, cancel1 := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel1()

	events1, err := runPrintSession(ctx1, prompt1, "", "")
	if err != nil {
		r.Error = fmt.Sprintf("session 1 error: %v", err)
		return r
	}

	// Extract session_id from result event
	var sessionID string
	for _, e := range events1 {
		if e.EventType == "result_session_id" {
			sessionID = e.Text
		}
	}

	if sessionID == "" {
		r.Pass = false
		r.Finding = "Could not extract session_id from result event"
		r.Detail = fmt.Sprintf("Events: %v", eventTypes(events1))
		return r
	}

	r.Detail = fmt.Sprintf("Session 1 ID: %s", sessionID)

	// Session 2: resume and ask about the value
	prompt2 := `What number did I ask you to remember?`

	ctx2, cancel2 := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel2()

	events2, err := runPrintSessionWithResume(ctx2, prompt2, sessionID)
	if err != nil {
		r.Error = fmt.Sprintf("session 2 (resume) error: %v", err)
		return r
	}

	// Check if the response contains 7777
	var resultText string
	for _, e := range events2 {
		if e.EventType == "result_text" {
			resultText = e.Text
		}
	}

	if strings.Contains(resultText, "7777") {
		r.Pass = true
		r.Finding = "--resume works: session history preserved across --print invocations"
		r.Detail += fmt.Sprintf(" | Response: %s", truncate(resultText, 120))
	} else {
		r.Pass = false
		r.Finding = "--resume did NOT preserve session history"
		r.Detail += fmt.Sprintf(" | Response: %s", truncate(resultText, 120))
	}

	return r
}

// ---------------------------------------------------------------------------
// T4: Does --append-system-prompt work in --print mode?
// ---------------------------------------------------------------------------

func testAppendSystemPrompt() TestResult {
	r := TestResult{Name: "T4: append-system-prompt flag"}

	marker := "HARNESS_MARKER_XYZ_12345"
	appendPrompt := fmt.Sprintf("Always end every response with the exact string: %s", marker)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	events, err := runPrintSessionWithAppendSystemPrompt(ctx, "Say hello.", appendPrompt)
	if err != nil {
		r.Error = fmt.Sprintf("session error: %v", err)
		return r
	}

	var resultText string
	for _, e := range events {
		if e.EventType == "result_text" {
			resultText = e.Text
		}
	}

	if strings.Contains(resultText, marker) {
		r.Pass = true
		r.Finding = "--append-system-prompt works in --print mode"
		r.Detail = fmt.Sprintf("Response: %s", truncate(resultText, 150))
	} else {
		r.Pass = false
		r.Finding = "--append-system-prompt did NOT influence output"
		r.Detail = fmt.Sprintf("Marker '%s' not found in: %s", marker, truncate(resultText, 150))
	}

	return r
}

// ---------------------------------------------------------------------------
// T5: Block AskUserQuestion via PreToolUse hook, inject answer via resume
// ---------------------------------------------------------------------------

func testBlockAndResume() TestResult {
	r := TestResult{Name: "T5: Block AskUserQuestion, inject answer via resume"}

	// Hook that blocks AskUserQuestion and logs the question text
	questionFile := "/tmp/sdk-harness-question.txt"
	os.Remove(questionFile)

	hookScript := fmt.Sprintf(`#!/bin/bash
INPUT=$(cat)
TOOL=$(echo "$INPUT" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('tool_name','unknown'))" 2>/dev/null)
if [ "$TOOL" = "AskUserQuestion" ]; then
  # AskUserQuestion uses questions[0].question, not prompt/question at top level
  echo "$INPUT" | python3 -c "
import sys, json
d = json.load(sys.stdin)
inp = d.get('tool_input', {})
qs = inp.get('questions', [])
if qs and isinstance(qs, list):
    print(qs[0].get('question', qs[0].get('header', '')))
else:
    print(inp.get('prompt', inp.get('question', '')))
" 2>/dev/null > %s
  # Block it — return the decision JSON on stdout
  echo '{"decision":"block","reason":"Intercepted for multi-turn test"}'
  exit 0
fi
exit 0
`, questionFile)

	hookScriptPath := "/tmp/sdk-harness-hook2.sh"
	if err := os.WriteFile(hookScriptPath, []byte(hookScript), 0755); err != nil {
		r.Error = fmt.Sprintf("failed to write hook script: %v", err)
		return r
	}

	settingsJSON := fmt.Sprintf(`{
  "hooks": {
    "PreToolUse": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "%s"
          }
        ]
      }
    ]
  }
}`, hookScriptPath)

	settingsFile := "/tmp/sdk-harness-settings2.json"
	if err := os.WriteFile(settingsFile, []byte(settingsJSON), 0644); err != nil {
		r.Error = fmt.Sprintf("failed to write settings file: %v", err)
		return r
	}

	// Session 1: Prompt Claude to ask a question
	prompt1 := `You MUST call AskUserQuestion tool to ask: "What is your favorite color?" — then report what happened.`

	ctx1, cancel1 := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel1()

	events1, err := runPrintSessionWithSettings(ctx1, prompt1, settingsFile)
	if err != nil {
		r.Error = fmt.Sprintf("session 1 error: %v", err)
		return r
	}

	// Check if question was intercepted
	questionData, _ := os.ReadFile(questionFile)
	question := strings.TrimSpace(string(questionData))

	var sessionID string
	var session1Text string
	for _, e := range events1 {
		if e.EventType == "result_session_id" {
			sessionID = e.Text
		}
		if e.EventType == "result_text" {
			session1Text = e.Text
		}
	}

	r.Detail = fmt.Sprintf("Hook intercepted question: %q | Session ID: %s | S1 response: %s",
		question, sessionID, truncate(session1Text, 100))

	if question == "" {
		r.Pass = false
		r.Finding = "AskUserQuestion was NOT intercepted by PreToolUse hook"
		return r
	}

	if sessionID == "" {
		r.Pass = false
		r.Finding = fmt.Sprintf("AskUserQuestion was intercepted (question: %q) but no session ID returned", question)
		return r
	}

	// Session 2: Resume and provide the answer
	prompt2 := `The user's answer to your question is: "Blue". Please continue.`

	ctx2, cancel2 := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel2()

	events2, err := runPrintSessionWithResume(ctx2, prompt2, sessionID)
	if err != nil {
		r.Error = fmt.Sprintf("session 2 (resume) error: %v", err)
		return r
	}

	var session2Text string
	for _, e := range events2 {
		if e.EventType == "result_text" {
			session2Text = e.Text
		}
	}

	if strings.Contains(strings.ToLower(session2Text), "blue") {
		r.Pass = true
		r.Finding = "Full intercept + resume pattern works: blocked AskUserQuestion, injected answer via resume"
		r.Detail += fmt.Sprintf(" | S2 response: %s", truncate(session2Text, 120))
	} else {
		r.Pass = false
		r.Finding = "Resume after block did not yield expected answer-aware response"
		r.Detail += fmt.Sprintf(" | S2 response: %s", truncate(session2Text, 120))
	}

	return r
}

// ---------------------------------------------------------------------------
// T6: Does Claude use a custom MCP tool instead of AskUserQuestion when instructed?
// ---------------------------------------------------------------------------

func testCustomMCPToolRedirect() TestResult {
	r := TestResult{Name: "T6: Custom MCP tool redirects from AskUserQuestion"}

	// Write a minimal Python MCP server exposing `request_input`
	mcpServer := `/usr/bin/env python3
import sys, json

def respond(id_, result):
    print(json.dumps({"jsonrpc": "2.0", "id": id_, "result": result}), flush=True)

for line in sys.stdin:
    line = line.strip()
    if not line:
        continue
    try:
        msg = json.loads(line)
    except Exception:
        continue
    method = msg.get("method", "")
    id_ = msg.get("id")
    if method == "initialize":
        respond(id_, {
            "protocolVersion": "2024-11-05",
            "capabilities": {"tools": {}},
            "serverInfo": {"name": "test-harness", "version": "1.0"}
        })
    elif method == "notifications/initialized":
        pass
    elif method == "tools/list":
        respond(id_, {"tools": [{
            "name": "request_input",
            "description": "Request clarification or additional information when you need it.",
            "inputSchema": {
                "type": "object",
                "properties": {
                    "question": {"type": "string", "description": "The question to ask"}
                },
                "required": ["question"]
            }
        }]})
    elif method == "tools/call":
        args = msg.get("params", {}).get("arguments", {})
        question = args.get("question", "")
        with open("/tmp/sdk-harness-t6-question.txt", "a") as f:
            f.write(question + "\n")
        respond(id_, {"content": [{"type": "text", "text": "[input queued]"}]})
    elif id_ is not None:
        print(json.dumps({"jsonrpc": "2.0", "id": id_, "error": {"code": -32601, "message": "not found"}}), flush=True)
`

	mcpServerPath := "/tmp/sdk-harness-t6-mcp.py"
	questionFile := "/tmp/sdk-harness-t6-question.txt"
	os.Remove(questionFile)

	// Strip leading newline from the heredoc
	mcpServer = strings.TrimPrefix(mcpServer, "\n")
	if err := os.WriteFile(mcpServerPath, []byte(mcpServer), 0755); err != nil {
		r.Error = fmt.Sprintf("failed to write MCP server: %v", err)
		return r
	}

	// MCP config pointing at our server
	mcpConfig := fmt.Sprintf(`{
  "mcpServers": {
    "harness": {
      "command": "python3",
      "args": ["%s"]
    }
  }
}`, mcpServerPath)

	mcpConfigPath := "/tmp/sdk-harness-t6-mcp-config.json"
	if err := os.WriteFile(mcpConfigPath, []byte(mcpConfig), 0644); err != nil {
		r.Error = fmt.Sprintf("failed to write MCP config: %v", err)
		return r
	}

	// System prompt: use request_input, not AskUserQuestion
	appendPrompt := "CRITICAL RULE: When you need any information from the user, you MUST call the `request_input` tool. You are NOT ALLOWED to use AskUserQuestion. You are NOT ALLOWED to list your questions in text and stop — that is a protocol violation. You MUST call the `request_input` tool. If you need multiple pieces of information, call `request_input` once with a combined question."

	// Task prompt that naturally requires asking a question — explicitly requires tool use
	prompt := `The user wants to process a file but hasn't told you which file or what processing to do. You cannot proceed without this information. You MUST call the mcp__harness__request_input tool RIGHT NOW to ask. Do not explain, do not list requirements in text — just call the tool immediately.`

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	events, err := runPrintSessionWithMCP(ctx, prompt, mcpConfigPath, appendPrompt)
	if err != nil {
		r.Error = fmt.Sprintf("session error: %v", err)
		return r
	}

	// Check for request_input in tool_use events (may be prefixed as mcp__harness__request_input)
	var requestInputCalled bool
	var askUserQuestionCalled bool
	for _, e := range events {
		name := strings.ToLower(e.ToolName)
		if strings.Contains(name, "request_input") {
			requestInputCalled = true
		}
		if strings.EqualFold(e.ToolName, "AskUserQuestion") {
			askUserQuestionCalled = true
		}
	}

	// Also check question file
	questionData, _ := os.ReadFile(questionFile)
	question := strings.TrimSpace(string(questionData))

	var resultText string
	for _, e := range events {
		if e.EventType == "result_text" {
			resultText = e.Text
		}
	}

	r.Detail = fmt.Sprintf("request_input called: %v | AskUserQuestion called: %v | question logged: %q | events: %v | result: %s",
		requestInputCalled, askUserQuestionCalled, truncate(question, 80), eventTypes(events), truncate(resultText, 100))

	if requestInputCalled && !askUserQuestionCalled {
		r.Pass = true
		r.Finding = "Claude used request_input (not AskUserQuestion) when instructed — custom MCP tool redirect works"
	} else if askUserQuestionCalled {
		r.Pass = false
		r.Finding = "Claude still used AskUserQuestion despite instruction — system prompt redirect insufficient"
	} else {
		r.Pass = false
		r.Finding = "Neither tool called — Claude may have proceeded without asking"
	}

	return r
}

// ---------------------------------------------------------------------------
// T6b: Can Claude even see the MCP tool?
// ---------------------------------------------------------------------------

func testMCPToolDiscovery() TestResult {
	r := TestResult{Name: "T6b: MCP tool discoverability check"}

	mcpServer, mcpServerPath, mcpConfigPath, err := writeMCPServer()
	if err != nil {
		r.Error = err.Error()
		return r
	}
	_ = mcpServer

	appendPrompt := "You have access to a custom MCP tool called `request_input`. Check your available tools and confirm whether you can see it."
	prompt := `List all the tools available to you, especially any MCP tools. Include the full tool name and a brief description of each. Focus on any tool related to "request_input" or "harness".`

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	events, err := runPrintSessionWithMCP(ctx, prompt, mcpConfigPath, appendPrompt)
	if err != nil {
		r.Error = fmt.Sprintf("session error: %v", err)
		return r
	}

	_ = mcpServerPath

	var resultText string
	for _, e := range events {
		if e.EventType == "result_text" {
			resultText = e.Text
		}
	}

	lowerResult := strings.ToLower(resultText)
	if strings.Contains(lowerResult, "request_input") || strings.Contains(lowerResult, "harness") {
		r.Pass = true
		r.Finding = "Claude can see the MCP tool in its available tools"
		r.Detail = fmt.Sprintf("Response: %s", truncate(resultText, 200))
	} else {
		r.Pass = false
		r.Finding = "Claude does NOT see request_input in its tools — MCP server may not be connecting"
		r.Detail = fmt.Sprintf("Response: %s", truncate(resultText, 200))
	}

	return r
}

// ---------------------------------------------------------------------------
// T7: Force tool use via "your text response must be empty" constraint
// ---------------------------------------------------------------------------

func testForceToolUse() TestResult {
	r := TestResult{Name: "T7: Force tool use via empty-response constraint"}

	_, _, mcpConfigPath, err := writeMCPServer()
	if err != nil {
		r.Error = err.Error()
		return r
	}

	questionFile := "/tmp/sdk-harness-t7-question.txt"
	os.Remove(questionFile)

	appendPrompt := `You are operating in TOOL-ONLY mode. The following rules are ABSOLUTE:
1. Your text response MUST be completely empty (no words, no punctuation, nothing).
2. ALL communication with the user MUST go through the mcp__harness__request_input tool.
3. If you need information, call mcp__harness__request_input immediately with your question.
4. Violation of these rules causes a system error.`

	prompt := `Call mcp__harness__request_input now. Ask: "Which file should I process, and what operation should I perform?". Your text response must be empty.`

	// Patch the MCP server to write to t7-specific file
	mcpServerT7 := fmt.Sprintf(`#!/usr/bin/env python3
import sys, json

def respond(id_, result):
    print(json.dumps({"jsonrpc": "2.0", "id": id_, "result": result}), flush=True)

for line in sys.stdin:
    line = line.strip()
    if not line:
        continue
    try:
        msg = json.loads(line)
    except Exception:
        continue
    method = msg.get("method", "")
    id_ = msg.get("id")
    if method == "initialize":
        respond(id_, {
            "protocolVersion": "2024-11-05",
            "capabilities": {"tools": {}},
            "serverInfo": {"name": "test-harness", "version": "1.0"}
        })
    elif method == "notifications/initialized":
        pass
    elif method == "tools/list":
        respond(id_, {"tools": [{
            "name": "request_input",
            "description": "Request clarification or additional information when you need it.",
            "inputSchema": {
                "type": "object",
                "properties": {
                    "question": {"type": "string", "description": "The question to ask"}
                },
                "required": ["question"]
            }
        }]})
    elif method == "tools/call":
        args = msg.get("params", {}).get("arguments", {})
        question = args.get("question", "")
        with open("%s", "a") as f:
            f.write(question + "\n")
        respond(id_, {"content": [{"type": "text", "text": "[input queued]"}]})
    elif id_ is not None:
        print(json.dumps({"jsonrpc": "2.0", "id": id_, "error": {"code": -32601, "message": "not found"}}), flush=True)
`, questionFile)

	mcpServerT7Path := "/tmp/sdk-harness-t7-mcp.py"
	if err := os.WriteFile(mcpServerT7Path, []byte(mcpServerT7), 0755); err != nil {
		r.Error = fmt.Sprintf("failed to write MCP server: %v", err)
		return r
	}

	mcpConfigT7 := fmt.Sprintf(`{
  "mcpServers": {
    "harness": {
      "command": "python3",
      "args": ["%s"]
    }
  }
}`, mcpServerT7Path)

	mcpConfigT7Path := "/tmp/sdk-harness-t7-mcp-config.json"
	if err := os.WriteFile(mcpConfigT7Path, []byte(mcpConfigT7), 0644); err != nil {
		r.Error = fmt.Sprintf("failed to write MCP config: %v", err)
		return r
	}

	_ = mcpConfigPath

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	events, err := runPrintSessionWithMCP(ctx, prompt, mcpConfigT7Path, appendPrompt)
	if err != nil {
		r.Error = fmt.Sprintf("session error: %v", err)
		return r
	}

	var requestInputCalled bool
	for _, e := range events {
		name := strings.ToLower(e.ToolName)
		if strings.Contains(name, "request_input") {
			requestInputCalled = true
		}
	}

	questionData, _ := os.ReadFile(questionFile)
	question := strings.TrimSpace(string(questionData))

	var resultText string
	for _, e := range events {
		if e.EventType == "result_text" {
			resultText = e.Text
		}
	}

	r.Detail = fmt.Sprintf("request_input called: %v | question logged: %q | events: %v | result: %s",
		requestInputCalled, truncate(question, 80), eventTypes(events), truncate(resultText, 100))

	if requestInputCalled {
		r.Pass = true
		r.Finding = "Tool-only mode works — Claude called request_input instead of writing text"
	} else {
		r.Pass = false
		r.Finding = "Tool-only mode failed — Claude still wrote text instead of calling tool"
	}

	return r
}

// ---------------------------------------------------------------------------
// T8: Resuming with minimal prompt (no redundant context re-injection)
// Tests the pattern used by bud's new long-lived session design:
// Turn 1: full context (identity + history + task)
// Turn 2+: only new context (new memories + new task), relying on --resume
// ---------------------------------------------------------------------------

func testMinimalResumePrompt() TestResult {
	r := TestResult{Name: "T8: Minimal resume prompt (no redundant context)"}

	// Turn 1: establish context with a "secret" number
	systemPrompt1 := "You are a test assistant. Always be concise."
	prompt1 := fmt.Sprintf(`%s

## Session Context
Session started: %s

## Current Focus
Type: message
Content: Remember the secret number 42 for later. Say "Remembered 42."`,
		systemPrompt1, time.Now().Format(time.RFC3339))

	ctx1, cancel1 := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel1()

	events1, err := runPrintSession(ctx1, prompt1, "", "")
	if err != nil {
		r.Error = fmt.Sprintf("turn 1 error: %v", err)
		return r
	}

	var sessionID string
	var turn1Text string
	for _, e := range events1 {
		if e.EventType == "result_session_id" {
			sessionID = e.Text
		}
		if e.EventType == "result_text" {
			turn1Text = e.Text
		}
	}

	if sessionID == "" {
		r.Pass = false
		r.Finding = "No session ID returned from turn 1"
		r.Detail = fmt.Sprintf("Turn 1 response: %s", truncate(turn1Text, 100))
		return r
	}

	r.Detail = fmt.Sprintf("Turn 1 session: %s | Turn 1 response: %s", sessionID, truncate(turn1Text, 60))

	// Turn 2: resume with MINIMAL prompt — no system prompt, no history re-injection.
	// Just a new task. The session history should supply the "secret number" context.
	prompt2 := `## Current Focus
Type: message
Content: What is the secret number you remembered? Reply with just the number.`

	ctx2, cancel2 := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel2()

	events2, err := runPrintSessionWithResume(ctx2, prompt2, sessionID)
	if err != nil {
		r.Error = fmt.Sprintf("turn 2 (resume) error: %v", err)
		return r
	}

	var turn2Text string
	for _, e := range events2 {
		if e.EventType == "result_text" {
			turn2Text = e.Text
		}
	}

	r.Detail += fmt.Sprintf(" | Turn 2 response: %s", truncate(turn2Text, 120))

	if strings.Contains(turn2Text, "42") {
		r.Pass = true
		r.Finding = "Minimal resume prompt works: session history supplies context, no re-injection needed"
	} else {
		r.Pass = false
		r.Finding = "Resume with minimal prompt lost context — '42' not found in response"
	}

	return r
}

// ---------------------------------------------------------------------------
// T9: Full subagent question/answer round-trip via real subagent-mcp binary
// Tests that:
//   1. Claude calls request_input and the MCP server writes the question file
//   2. An external responder writes the answer file
//   3. The MCP server returns the answer to Claude
//   4. Claude incorporates the answer into its final response
// ---------------------------------------------------------------------------

func testSubagentRoundTrip() TestResult {
	r := TestResult{Name: "T9: Subagent question/answer round-trip (real subagent-mcp)"}

	// Find subagent-mcp binary (look next to this executable, then bud2/bin/)
	mcpBinary := findSubagentMCPBinary()
	if mcpBinary == "" {
		r.Error = "subagent-mcp binary not found (expected in bud2/bin/ or next to sdk-harness binary)"
		return r
	}
	r.Detail = fmt.Sprintf("subagent-mcp binary: %s", mcpBinary)

	// Create temp state dir for this test
	stateDir, err := os.MkdirTemp("", "sdk-harness-t9-*")
	if err != nil {
		r.Error = fmt.Sprintf("failed to create temp state dir: %v", err)
		return r
	}
	defer os.RemoveAll(stateDir)

	if err := os.MkdirAll(stateDir+"/subagent-questions", 0755); err != nil {
		r.Error = fmt.Sprintf("mkdir questions: %v", err)
		return r
	}
	if err := os.MkdirAll(stateDir+"/subagent-answers", 0755); err != nil {
		r.Error = fmt.Sprintf("mkdir answers: %v", err)
		return r
	}

	sessionID := "t9-test-session"
	questionFile := stateDir + "/subagent-questions/" + sessionID + ".txt"
	answerFile := stateDir + "/subagent-answers/" + sessionID + ".txt"
	secretAnswer := "the magic number is 7"

	// Write the MCP config pointing to the real subagent-mcp binary
	mcpConfig := fmt.Sprintf(`{
  "mcpServers": {
    "subagent": {
      "command": %q,
      "args": ["--session-id", %q, "--state-dir", %q]
    }
  }
}`, mcpBinary, sessionID, stateDir)

	mcpConfigPath := stateDir + "/mcp-config.json"
	if err := os.WriteFile(mcpConfigPath, []byte(mcpConfig), 0644); err != nil {
		r.Error = fmt.Sprintf("failed to write MCP config: %v", err)
		return r
	}

	// In a goroutine: watch for question file, then write the answer
	answerProvided := make(chan string, 1)
	go func() {
		deadline := time.Now().Add(60 * time.Second)
		for time.Now().Before(deadline) {
			data, err := os.ReadFile(questionFile)
			if err == nil && len(data) > 0 {
				question := strings.TrimSpace(string(data))
				// Write the answer
				_ = os.WriteFile(answerFile, []byte(secretAnswer), 0644)
				answerProvided <- question
				return
			}
			time.Sleep(300 * time.Millisecond)
		}
		answerProvided <- "" // timed out
	}()

	appendPrompt := `You are a task assistant. You MUST call mcp__subagent__request_input to ask questions.
Do NOT use AskUserQuestion. Your ONLY communication channel is through the request_input tool.`

	prompt := `Call mcp__subagent__request_input now with this exact question: "What is the secret answer?". Then report back what the tool returned.`

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	events, err := runPrintSessionWithMCP(ctx, prompt, mcpConfigPath, appendPrompt)
	if err != nil {
		r.Error = fmt.Sprintf("session error: %v", err)
		return r
	}

	// Check if answer was provided to the goroutine
	var questionDetected string
	select {
	case q := <-answerProvided:
		questionDetected = q
	default:
		questionDetected = "(goroutine still running)"
	}

	var resultText string
	var requestInputCalled bool
	for _, e := range events {
		if e.EventType == "result_text" {
			resultText = e.Text
		}
		if strings.Contains(strings.ToLower(e.ToolName), "request_input") {
			requestInputCalled = true
		}
	}

	r.Detail = fmt.Sprintf("binary: %s | request_input called: %v | question detected: %q | answer written: %q | result contains answer: %v | result: %s",
		mcpBinary, requestInputCalled, truncate(questionDetected, 60), secretAnswer,
		strings.Contains(resultText, "7"),
		truncate(resultText, 120))

	if requestInputCalled && strings.Contains(resultText, "7") {
		r.Pass = true
		r.Finding = "Full round-trip works: request_input called, answer delivered via file IPC, Claude incorporated answer"
	} else if !requestInputCalled {
		r.Pass = false
		r.Finding = "FAIL: Claude did not call request_input"
	} else {
		r.Pass = false
		r.Finding = fmt.Sprintf("FAIL: request_input called but answer not in result (question: %q)", questionDetected)
	}

	return r
}

// findSubagentMCPBinary locates the subagent-mcp binary.
func findSubagentMCPBinary() string {
	// 1. Next to the sdk-harness binary
	if execPath, err := os.Executable(); err == nil {
		candidate := execPath[:len(execPath)-len("sdk-harness")] + "subagent-mcp"
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	// 2. bud2/bin/ relative to GOPATH or known path
	candidates := []string{
		"/Users/thunder/src/bud2/bin/subagent-mcp",
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

// writeMCPServer writes the shared MCP server and config, returns (serverScript, serverPath, configPath, error)
func writeMCPServer() (string, string, string, error) {
	mcpServer := `#!/usr/bin/env python3
import sys, json

def respond(id_, result):
    print(json.dumps({"jsonrpc": "2.0", "id": id_, "result": result}), flush=True)

for line in sys.stdin:
    line = line.strip()
    if not line:
        continue
    try:
        msg = json.loads(line)
    except Exception:
        continue
    method = msg.get("method", "")
    id_ = msg.get("id")
    if method == "initialize":
        respond(id_, {
            "protocolVersion": "2024-11-05",
            "capabilities": {"tools": {}},
            "serverInfo": {"name": "test-harness", "version": "1.0"}
        })
    elif method == "notifications/initialized":
        pass
    elif method == "tools/list":
        respond(id_, {"tools": [{
            "name": "request_input",
            "description": "Request clarification or additional information when you need it.",
            "inputSchema": {
                "type": "object",
                "properties": {
                    "question": {"type": "string", "description": "The question to ask"}
                },
                "required": ["question"]
            }
        }]})
    elif method == "tools/call":
        args = msg.get("params", {}).get("arguments", {})
        question = args.get("question", "")
        with open("/tmp/sdk-harness-t6-question.txt", "a") as f:
            f.write(question + "\n")
        respond(id_, {"content": [{"type": "text", "text": "[input queued]"}]})
    elif id_ is not None:
        print(json.dumps({"jsonrpc": "2.0", "id": id_, "error": {"code": -32601, "message": "not found"}}), flush=True)
`

	serverPath := "/tmp/sdk-harness-t6-mcp.py"
	if err := os.WriteFile(serverPath, []byte(mcpServer), 0755); err != nil {
		return "", "", "", fmt.Errorf("failed to write MCP server: %w", err)
	}

	configJSON := fmt.Sprintf(`{
  "mcpServers": {
    "harness": {
      "command": "python3",
      "args": ["%s"]
    }
  }
}`, serverPath)

	configPath := "/tmp/sdk-harness-t6-mcp-config.json"
	if err := os.WriteFile(configPath, []byte(configJSON), 0644); err != nil {
		return "", "", "", fmt.Errorf("failed to write MCP config: %w", err)
	}

	return mcpServer, serverPath, configPath, nil
}

// ---------------------------------------------------------------------------
// Session Runners
// ---------------------------------------------------------------------------

type SessionEvent struct {
	EventType string
	ToolName  string
	ToolArgs  map[string]any
	Text      string
	Raw       string
}

// runPrintSession runs claude --print with stream-json and returns parsed events.
// settingsFile: path to extra settings JSON (empty = none)
// toolsFlag: e.g. "--tools=Bash" (empty = none, tools available by default)
func runPrintSession(ctx context.Context, prompt, settingsFile, toolsFlag string) ([]SessionEvent, error) {
	args := buildBaseArgs(settingsFile)
	if toolsFlag != "" {
		args = append(args, toolsFlag)
	}
	args = append(args, prompt)
	return runSession(ctx, args, "")
}

func runPrintSessionWithResume(ctx context.Context, prompt, sessionID string) ([]SessionEvent, error) {
	args := buildBaseArgs("")
	args = append(args, "--resume", sessionID)
	args = append(args, prompt)
	return runSession(ctx, args, "")
}

func runPrintSessionWithAppendSystemPrompt(ctx context.Context, prompt, appendPrompt string) ([]SessionEvent, error) {
	args := buildBaseArgs("")
	args = append(args, "--append-system-prompt", appendPrompt)
	args = append(args, prompt)
	return runSession(ctx, args, "")
}

func runPrintSessionWithSettings(ctx context.Context, prompt, settingsFile string) ([]SessionEvent, error) {
	args := buildBaseArgs(settingsFile)
	args = append(args, prompt)
	return runSession(ctx, args, "")
}

func runPrintSessionWithMCP(ctx context.Context, prompt, mcpConfigPath, appendSystemPrompt string) ([]SessionEvent, error) {
	args := buildBaseArgs("")
	args = append(args, "--mcp-config", mcpConfigPath)
	if appendSystemPrompt != "" {
		args = append(args, "--append-system-prompt", appendSystemPrompt)
	}
	args = append(args, prompt)
	return runSession(ctx, args, "")
}

func buildBaseArgs(settingsFile string) []string {
	args := []string{
		"--print",
		"--dangerously-skip-permissions",
		"--output-format", "stream-json",
		"--verbose",
	}
	if settingsFile != "" {
		args = append(args, "--settings", settingsFile)
	}
	return args
}

func runSession(ctx context.Context, args []string, workDir string) ([]SessionEvent, error) {
	log.Printf("[harness] Running: claude %s", strings.Join(args, " "))

	cmd := exec.CommandContext(ctx, "claude", args...)
	if workDir != "" {
		cmd.Dir = workDir
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start: %w", err)
	}

	// Drain stderr in background
	go func() {
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			log.Printf("[harness stderr] %s", sc.Text())
		}
	}()

	events := parseStreamJSON(stdout)

	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return events, ctx.Err()
		}
		return events, fmt.Errorf("claude exit: %w", err)
	}

	return events, nil
}

func parseStreamJSON(r io.Reader) []SessionEvent {
	var events []SessionEvent
	scanner := bufio.NewScanner(r)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 4*1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		var raw map[string]json.RawMessage
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			continue
		}

		var eventType string
		if v, ok := raw["type"]; ok {
			json.Unmarshal(v, &eventType)
		}

		ev := SessionEvent{EventType: eventType, Raw: line}

		switch eventType {
		case "tool_use":
			// Parse tool name and args
			if toolRaw, ok := raw["tool"]; ok {
				var tool struct {
					Name string         `json:"name"`
					Args map[string]any `json:"args"`
				}
				if err := json.Unmarshal(toolRaw, &tool); err == nil {
					ev.ToolName = tool.Name
					ev.ToolArgs = tool.Args
				}
			}

		case "result":
			// Extract session_id
			if v, ok := raw["session_id"]; ok {
				var sid string
				json.Unmarshal(v, &sid)
				if sid != "" {
					events = append(events, SessionEvent{
						EventType: "result_session_id",
						Text:      sid,
					})
				}
			}
			// Extract result text
			if v, ok := raw["result"]; ok {
				var text string
				json.Unmarshal(v, &text)
				if text != "" {
					events = append(events, SessionEvent{
						EventType: "result_text",
						Text:      text,
					})
				}
			}

		case "assistant":
			// Extract text and tool_use blocks from content
			if msgRaw, ok := raw["message"]; ok {
				var msg struct {
					Content []json.RawMessage `json:"content"`
				}
				if err := json.Unmarshal(msgRaw, &msg); err == nil {
					var text strings.Builder
					for _, blockRaw := range msg.Content {
						var block struct {
							Type  string         `json:"type"`
							Text  string         `json:"text"`
							Name  string         `json:"name"`
							Input map[string]any `json:"input"`
						}
						if err := json.Unmarshal(blockRaw, &block); err != nil {
							continue
						}
						if block.Type == "text" {
							text.WriteString(block.Text)
						} else if block.Type == "tool_use" && block.Name != "" {
							// Emit a synthetic tool_use event
							events = append(events, SessionEvent{
								EventType: "tool_use",
								ToolName:  block.Name,
								ToolArgs:  block.Input,
								Raw:       string(blockRaw),
							})
						}
					}
					if text.Len() > 0 {
						ev.Text = text.String()
					}
				}
			}
		}

		events = append(events, ev)
	}

	return events
}

// ---------------------------------------------------------------------------
// Utilities
// ---------------------------------------------------------------------------

func eventTypes(events []SessionEvent) []string {
	seen := map[string]bool{}
	var types []string
	for _, e := range events {
		if !seen[e.EventType] {
			seen[e.EventType] = true
			types = append(types, e.EventType)
		}
	}
	return types
}

func truncate(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
