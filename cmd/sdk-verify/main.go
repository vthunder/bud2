// cmd/sdk-verify/main.go
//
// Verification harness for the severity1/claude-agent-sdk-go migration.
// Tests three critical questions before implementing the full refactor:
//
//	V1: Can WithPreToolUseHook (via WithClient) block and inject a response to Claude?
//	V2: What are the exact keys in ResultMessage.Usage?
//	V3: Does WithResume work end-to-end via the SDK?
//
// IMPORTANT: Hooks/CanUseTool only work with WithClient (streaming mode).
// Query() uses one-shot mode (closeStdin=true) which skips control protocol setup.
//
// Run: go run ./cmd/sdk-verify/
package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	claudecode "github.com/severity1/claude-agent-sdk-go"
)

func main() {
	fmt.Println("=== SDK Verification Harness ===")
	fmt.Println()

	type test struct {
		name string
		fn   func() result
	}

	tests := []test{
		{"V1a: WithClient + PreToolUseHook — Reason injection", testV1HookWithClientReason},
		{"V1b: WithClient + PreToolUseHook — SystemMessage injection", testV1HookWithClientSystemMessage},
		{"V1c: WithClient + CanUseTool blocking", testV1CanUseToolWithClient},
		{"V2:  ResultMessage.Usage keys (WithClient)", testV2UsageKeys},
		{"V3:  WithResume via Query()", testV3Resume},
		{"V3b: WithResume via WithClient", testV3ResumeWithClient},
	}

	var results []result
	for _, t := range tests {
		fmt.Printf("--- %s ---\n", t.name)
		r := t.fn()
		r.name = t.name
		results = append(results, r)
		printResult(r)
		fmt.Println()
	}

	fmt.Println("=== Summary ===")
	for _, r := range results {
		status := "✓ PASS"
		if !r.pass {
			status = "✗ FAIL"
		}
		fmt.Printf("%s  %s\n", status, r.name)
		if r.finding != "" {
			fmt.Printf("       Finding: %s\n", r.finding)
		}
	}
}

type result struct {
	name    string
	pass    bool
	finding string
	detail  string
	errMsg  string
}

func printResult(r result) {
	if r.pass {
		fmt.Printf("  PASS\n")
	} else {
		fmt.Printf("  FAIL\n")
	}
	if r.finding != "" {
		fmt.Printf("  Finding: %s\n", r.finding)
	}
	if r.detail != "" {
		fmt.Printf("  Detail:  %s\n", r.detail)
	}
	if r.errMsg != "" {
		fmt.Printf("  Error:   %s\n", r.errMsg)
	}
}

func ptr[T any](v T) *T { return &v }

// isIgnorableIterError returns true for non-fatal errors from iter.Next/ReceiveMessages
// that should be skipped rather than treated as session failures.
func isIgnorableIterError(err error) bool {
	if err == nil {
		return true
	}
	msg := err.Error()
	// rate_limit_event and similar unknown event types are non-fatal
	if strings.Contains(msg, "unknown message type") {
		return true
	}
	return false
}

// drainMessages collects all text and the ResultMessage from a client's ReceiveMessages channel.
func drainMessages(ctx context.Context, ch <-chan claudecode.Message) (text string, sessionID string, usage *map[string]any) {
	var sb strings.Builder
	for msg := range ch {
		switch m := msg.(type) {
		case *claudecode.AssistantMessage:
			for _, block := range m.Content {
				if tb, ok := block.(*claudecode.TextBlock); ok {
					sb.WriteString(tb.Text)
				}
			}
		case *claudecode.ResultMessage:
			sessionID = m.SessionID
			usage = m.Usage
		}
	}
	return sb.String(), sessionID, usage
}

// ---------------------------------------------------------------------------
// V1a: WithClient + PreToolUseHook — block for 2s, inject via Reason field
// ---------------------------------------------------------------------------

func testV1HookWithClientReason() result {
	r := result{}

	var hookFired bool

	hook := claudecode.WithPreToolUseHook("AskUserQuestion",
		func(ctx context.Context, input any, toolUseID *string, hookCtx claudecode.HookContext) (claudecode.HookJSONOutput, error) {
			hookFired = true
			// Simulate 2s async wait
			select {
			case <-time.After(2 * time.Second):
			case <-ctx.Done():
				return claudecode.HookJSONOutput{}, ctx.Err()
			}
			decision := "block"
			reason := "The answer to your question is: INJECTION_ANSWER_BLUE"
			return claudecode.HookJSONOutput{
				Decision: &decision,
				Reason:   &reason,
			}, nil
		})

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	var finalText strings.Builder
	var sessionID string

	err := claudecode.WithClient(ctx, func(client claudecode.Client) error {
		if err := client.Query(ctx,
			`You MUST call AskUserQuestion tool asking "What color do I want?". Then tell me what color you learned.`); err != nil {
			return err
		}
		msgs := client.ReceiveMessages(ctx)
		for msg := range msgs {
			switch m := msg.(type) {
			case *claudecode.AssistantMessage:
				for _, block := range m.Content {
					if tb, ok := block.(*claudecode.TextBlock); ok {
						finalText.WriteString(tb.Text)
					}
				}
			case *claudecode.ResultMessage:
				sessionID = m.SessionID
			}
		}
		return nil
	},
		claudecode.WithPermissionMode(claudecode.PermissionModeBypassPermissions),
		claudecode.WithDebugDisabled(),
		hook,
	)
	if err != nil && !isIgnorableIterError(err) {
		r.errMsg = fmt.Sprintf("WithClient: %v", err)
	}

	text := finalText.String()
	r.detail = fmt.Sprintf("hookFired=%v sessionID=%s text=%s", hookFired, sessionID, trunc(text, 200))

	if !hookFired {
		r.finding = "Hook did NOT fire — AskUserQuestion not intercepted via WithClient"
		return r
	}

	if strings.Contains(strings.ToLower(text), "blue") || strings.Contains(text, "INJECTION_ANSWER_BLUE") {
		r.pass = true
		r.finding = "WithClient + hook block + Reason field → Claude received the injected answer"
	} else {
		r.pass = false
		r.finding = "Hook fired and blocked, but Reason NOT received as answer"
	}
	return r
}

// ---------------------------------------------------------------------------
// V1b: WithClient + PreToolUseHook — block + SystemMessage injection
// ---------------------------------------------------------------------------

func testV1HookWithClientSystemMessage() result {
	r := result{}

	var hookFired bool

	hook := claudecode.WithPreToolUseHook("AskUserQuestion",
		func(ctx context.Context, input any, toolUseID *string, hookCtx claudecode.HookContext) (claudecode.HookJSONOutput, error) {
			hookFired = true
			select {
			case <-time.After(1 * time.Second):
			case <-ctx.Done():
				return claudecode.HookJSONOutput{}, ctx.Err()
			}
			decision := "block"
			sysMsg := "USER_SYSTEM_MSG: The answer is SYSTEMMSG_PURPLE"
			return claudecode.HookJSONOutput{
				Decision:      &decision,
				SystemMessage: &sysMsg,
			}, nil
		})

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	var finalText strings.Builder

	err := claudecode.WithClient(ctx, func(client claudecode.Client) error {
		if err := client.Query(ctx,
			`You MUST call AskUserQuestion asking "What color?" then tell me the color you learned.`); err != nil {
			return err
		}
		for msg := range client.ReceiveMessages(ctx) {
			if m, ok := msg.(*claudecode.AssistantMessage); ok {
				for _, block := range m.Content {
					if tb, ok := block.(*claudecode.TextBlock); ok {
						finalText.WriteString(tb.Text)
					}
				}
			}
		}
		return nil
	},
		claudecode.WithPermissionMode(claudecode.PermissionModeBypassPermissions),
		claudecode.WithDebugDisabled(),
		hook,
	)
	if err != nil && !isIgnorableIterError(err) {
		r.errMsg = fmt.Sprintf("WithClient: %v", err)
	}

	text := finalText.String()
	r.detail = fmt.Sprintf("hookFired=%v text=%s", hookFired, trunc(text, 200))

	if !hookFired {
		r.finding = "Hook did NOT fire via WithClient"
		return r
	}

	if strings.Contains(strings.ToLower(text), "purple") || strings.Contains(text, "SYSTEMMSG_PURPLE") {
		r.pass = true
		r.finding = "SystemMessage field received by Claude as answer"
	} else {
		r.pass = false
		r.finding = "SystemMessage NOT received by Claude as answer"
	}
	return r
}

// ---------------------------------------------------------------------------
// V1c: WithClient + CanUseTool — blocking deny with message
// ---------------------------------------------------------------------------

func testV1CanUseToolWithClient() result {
	r := result{}

	var callbackFired bool
	answerCh := make(chan string, 1)

	go func() {
		time.Sleep(1 * time.Second)
		answerCh <- "CANTOOL_GREEN"
	}()

	canUse := claudecode.WithCanUseTool(func(
		ctx context.Context,
		toolName string,
		input map[string]any,
		permCtx claudecode.ToolPermissionContext,
	) (claudecode.PermissionResult, error) {
		if toolName != "AskUserQuestion" {
			return claudecode.NewPermissionResultAllow(), nil
		}
		callbackFired = true
		select {
		case answer := <-answerCh:
			return claudecode.NewPermissionResultDeny(fmt.Sprintf("The answer is %s", answer)), nil
		case <-ctx.Done():
			return claudecode.NewPermissionResultDeny("cancelled"), nil
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	var finalText strings.Builder

	err := claudecode.WithClient(ctx, func(client claudecode.Client) error {
		if err := client.Query(ctx,
			`You MUST call AskUserQuestion asking "What color?" then tell me the color you learned.`); err != nil {
			return err
		}
		for msg := range client.ReceiveMessages(ctx) {
			if m, ok := msg.(*claudecode.AssistantMessage); ok {
				for _, block := range m.Content {
					if tb, ok := block.(*claudecode.TextBlock); ok {
						finalText.WriteString(tb.Text)
					}
				}
			}
		}
		return nil
	},
		claudecode.WithPermissionMode(claudecode.PermissionModeBypassPermissions),
		claudecode.WithDebugDisabled(),
		canUse,
	)
	if err != nil && !isIgnorableIterError(err) {
		r.errMsg = fmt.Sprintf("WithClient: %v", err)
	}

	text := finalText.String()
	r.detail = fmt.Sprintf("callbackFired=%v text=%s", callbackFired, trunc(text, 200))

	if !callbackFired {
		r.finding = "CanUseTool callback did NOT fire for AskUserQuestion"
		return r
	}

	if strings.Contains(strings.ToLower(text), "green") || strings.Contains(text, "CANTOOL_GREEN") {
		r.pass = true
		r.finding = "CanUseTool deny message reaches Claude — viable for answer injection"
	} else {
		r.pass = false
		r.finding = "CanUseTool fired and denied, but deny message NOT seen in Claude output"
	}
	return r
}

// ---------------------------------------------------------------------------
// V2: ResultMessage.Usage field keys (WithClient)
// ---------------------------------------------------------------------------

func testV2UsageKeys() result {
	r := result{}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	var usageKeys []string
	var usageValues []string

	err := claudecode.WithClient(ctx, func(client claudecode.Client) error {
		if err := client.Query(ctx, "Say exactly: hello"); err != nil {
			return err
		}
		for msg := range client.ReceiveMessages(ctx) {
			if rm, ok := msg.(*claudecode.ResultMessage); ok {
				if rm.Usage != nil {
					for k, v := range *rm.Usage {
						usageKeys = append(usageKeys, k)
						usageValues = append(usageValues, fmt.Sprintf("%s=%v", k, v))
					}
				}
			}
		}
		return nil
	},
		claudecode.WithPermissionMode(claudecode.PermissionModeBypassPermissions),
		claudecode.WithDebugDisabled(),
	)
	if err != nil && !isIgnorableIterError(err) {
		r.errMsg = fmt.Sprintf("WithClient: %v", err)
	}

	if len(usageKeys) == 0 {
		r.finding = "ResultMessage.Usage is nil or empty"
		r.errMsg += fmt.Sprintf(" (note: may have hit rate_limit_event before ResultMessage)")
		return r
	}

	keySet := make(map[string]bool)
	for _, k := range usageKeys {
		keySet[k] = true
	}

	expectedKeys := []string{"input_tokens", "output_tokens", "cache_creation_input_tokens", "cache_read_input_tokens"}
	var missing []string
	for _, k := range expectedKeys {
		if !keySet[k] {
			missing = append(missing, k)
		}
	}

	r.detail = fmt.Sprintf("Usage map: {%s}", strings.Join(usageValues, ", "))

	if len(missing) == 0 {
		r.pass = true
		r.finding = "All 4 expected keys present: input_tokens, output_tokens, cache_creation_input_tokens, cache_read_input_tokens"
	} else {
		r.pass = false
		r.finding = fmt.Sprintf("Missing keys: %v | Found keys: %v", missing, usageKeys)
	}
	return r
}

// ---------------------------------------------------------------------------
// V3: WithResume preserves session across SDK Query() calls
// ---------------------------------------------------------------------------

func testV3Resume() result {
	r := result{}

	ctx1, cancel1 := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel1()

	// Session A: establish a "secret"
	var sessionAID string
	var textA strings.Builder

	iter1, err := claudecode.Query(ctx1,
		`Remember the secret code ZXQV9981. Say exactly: "Stored ZXQV9981"`,
		claudecode.WithPermissionMode(claudecode.PermissionModeBypassPermissions),
		claudecode.WithDebugDisabled(),
	)
	if err != nil {
		r.errMsg = fmt.Sprintf("session A query: %v", err)
		return r
	}

	for {
		msg, err := iter1.Next(ctx1)
		if err != nil {
			if errors.Is(err, claudecode.ErrNoMoreMessages) {
				break
			}
			if isIgnorableIterError(err) {
				continue
			}
			r.errMsg = fmt.Sprintf("session A iter: %v", err)
			break
		}
		switch m := msg.(type) {
		case *claudecode.AssistantMessage:
			for _, block := range m.Content {
				if tb, ok := block.(*claudecode.TextBlock); ok {
					textA.WriteString(tb.Text)
				}
			}
		case *claudecode.ResultMessage:
			sessionAID = m.SessionID
		}
	}
	iter1.Close()

	if sessionAID == "" {
		r.finding = "Session A: no SessionID in ResultMessage"
		r.detail = fmt.Sprintf("Session A text: %s", trunc(textA.String(), 100))
		return r
	}

	r.detail = fmt.Sprintf("Session A ID: %s | A text: %s", sessionAID, trunc(textA.String(), 60))

	// Session B: resume and ask about the secret
	ctx2, cancel2 := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel2()

	var textB strings.Builder

	iter2, err := claudecode.Query(ctx2,
		`What secret code did you store? Reply with just the code.`,
		claudecode.WithPermissionMode(claudecode.PermissionModeBypassPermissions),
		claudecode.WithDebugDisabled(),
		claudecode.WithResume(sessionAID),
	)
	if err != nil {
		r.errMsg = fmt.Sprintf("session B query: %v", err)
		return r
	}

	for {
		msg, err := iter2.Next(ctx2)
		if err != nil {
			if errors.Is(err, claudecode.ErrNoMoreMessages) {
				break
			}
			if isIgnorableIterError(err) {
				continue
			}
			r.errMsg = fmt.Sprintf("session B iter: %v", err)
			break
		}
		if m, ok := msg.(*claudecode.AssistantMessage); ok {
			for _, block := range m.Content {
				if tb, ok := block.(*claudecode.TextBlock); ok {
					textB.WriteString(tb.Text)
				}
			}
		}
	}
	iter2.Close()

	b := textB.String()
	r.detail += fmt.Sprintf(" | B text: %s", trunc(b, 120))

	if strings.Contains(b, "ZXQV9981") {
		r.pass = true
		r.finding = "WithResume works via SDK — session history preserved"
	} else {
		r.pass = false
		r.finding = "WithResume did NOT preserve session — secret code not found in session B"
	}
	return r
}

// ---------------------------------------------------------------------------
// V3b: WithResume via WithClient (streaming mode)
// ---------------------------------------------------------------------------

func testV3ResumeWithClient() result {
	r := result{}

	ctx1, cancel1 := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel1()

	var sessionAID string
	var textA strings.Builder

	err := claudecode.WithClient(ctx1, func(client claudecode.Client) error {
		if err := client.Query(ctx1, `Remember the secret code ZXQV9981. Say exactly: "Stored ZXQV9981"`); err != nil {
			return err
		}
		for msg := range client.ReceiveMessages(ctx1) {
			switch m := msg.(type) {
			case *claudecode.AssistantMessage:
				for _, block := range m.Content {
					if tb, ok := block.(*claudecode.TextBlock); ok {
						textA.WriteString(tb.Text)
					}
				}
			case *claudecode.ResultMessage:
				sessionAID = m.SessionID
			}
		}
		return nil
	},
		claudecode.WithPermissionMode(claudecode.PermissionModeBypassPermissions),
		claudecode.WithDebugDisabled(),
	)
	if err != nil && !isIgnorableIterError(err) {
		r.errMsg = fmt.Sprintf("session A: %v", err)
	}

	if sessionAID == "" {
		r.finding = "Session A: no SessionID in ResultMessage (WithClient mode)"
		r.detail = fmt.Sprintf("Session A text: %s", trunc(textA.String(), 100))
		return r
	}

	r.detail = fmt.Sprintf("Session A ID: %s | A text: %s", sessionAID, trunc(textA.String(), 60))

	ctx2, cancel2 := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel2()

	var textB strings.Builder

	err = claudecode.WithClient(ctx2, func(client claudecode.Client) error {
		if err := client.Query(ctx2, `What secret code did you store? Reply with just the code.`); err != nil {
			return err
		}
		for msg := range client.ReceiveMessages(ctx2) {
			if m, ok := msg.(*claudecode.AssistantMessage); ok {
				for _, block := range m.Content {
					if tb, ok := block.(*claudecode.TextBlock); ok {
						textB.WriteString(tb.Text)
					}
				}
			}
		}
		return nil
	},
		claudecode.WithPermissionMode(claudecode.PermissionModeBypassPermissions),
		claudecode.WithDebugDisabled(),
		claudecode.WithResume(sessionAID),
	)
	if err != nil && !isIgnorableIterError(err) {
		r.errMsg += fmt.Sprintf(" session B: %v", err)
	}

	b := textB.String()
	r.detail += fmt.Sprintf(" | B text: %s", trunc(b, 120))

	if strings.Contains(b, "ZXQV9981") {
		r.pass = true
		r.finding = "WithResume via WithClient works — session history preserved"
	} else {
		r.pass = false
		r.finding = "WithResume via WithClient did NOT preserve session"
	}
	return r
}

// ---------------------------------------------------------------------------
// Utilities
// ---------------------------------------------------------------------------

func trunc(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
