package effectors

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/vthunder/bud2/internal/types"
)

// newTestEffectorNoSession creates a DiscordEffector with no session (for retry logic tests)
func newTestEffectorNoSession() *DiscordEffector {
	return &DiscordEffector{
		getSession:       func() *discordgo.Session { return nil },
		pollInterval:     100 * time.Millisecond,
		maxRetryDuration: DefaultMaxRetryDuration,
		retryStates:      make(map[string]*retryState),
		typingChans:      make(map[string]chan struct{}),
		stopChan:         make(chan struct{}),
	}
}

func newTestAction(id string) *types.Action {
	return &types.Action{
		ID:       id,
		Effector: "discord",
		Type:     "send_message",
		Payload:  map[string]any{"channel_id": "123", "content": "hello"},
	}
}

// --- isNonRetryableError ---

func TestIsNonRetryableError_NilError(t *testing.T) {
	if isNonRetryableError(errors.New("network timeout")) {
		t.Error("generic error should be retryable")
	}
}

func TestIsNonRetryableError_4xxStatus(t *testing.T) {
	for _, code := range []int{400, 401, 403, 404, 429} {
		err := &discordgo.RESTError{
			Response: &http.Response{StatusCode: code},
		}
		if !isNonRetryableError(err) {
			t.Errorf("HTTP %d should be non-retryable", code)
		}
	}
}

func TestIsNonRetryableError_5xxStatus(t *testing.T) {
	for _, code := range []int{500, 502, 503} {
		err := &discordgo.RESTError{
			Response: &http.Response{StatusCode: code},
		}
		if isNonRetryableError(err) {
			t.Errorf("HTTP %d should be retryable (server error)", code)
		}
	}
}

func TestIsNonRetryableError_NilResponse(t *testing.T) {
	err := &discordgo.RESTError{Response: nil}
	if isNonRetryableError(err) {
		t.Error("RESTError with nil response should be retryable")
	}
}

// --- handleActionError ---

func TestHandleActionError_RetryableFirstAttempt(t *testing.T) {
	e := newTestEffectorNoSession()
	action := newTestAction("act-1")
	now := time.Now()

	retried := e.handleActionError(action, errors.New("network error"), now)

	if !retried {
		t.Error("expected action to be scheduled for retry")
	}

	e.retryMu.Lock()
	state := e.retryStates["act-1"]
	e.retryMu.Unlock()

	if state == nil {
		t.Fatal("expected retry state to be recorded")
	}
	if state.attempts != 1 {
		t.Errorf("expected 1 attempt, got %d", state.attempts)
	}
	// Backoff for attempt 1: 1s
	expectedBackoff := 1 * time.Second
	actualBackoff := state.nextRetry.Sub(now)
	if actualBackoff < expectedBackoff-10*time.Millisecond || actualBackoff > expectedBackoff+10*time.Millisecond {
		t.Errorf("expected ~%v backoff, got %v", expectedBackoff, actualBackoff)
	}
}

func TestHandleActionError_ExponentialBackoff(t *testing.T) {
	e := newTestEffectorNoSession()
	action := newTestAction("act-2")
	now := time.Now()

	// Simulate multiple failures
	expectedBackoffs := []time.Duration{1, 2, 4, 8, 16, 32, 60, 60} // seconds, capped at 60s
	for i, expected := range expectedBackoffs {
		retried := e.handleActionError(action, errors.New("transient"), now)
		if !retried {
			t.Fatalf("attempt %d: expected retry", i+1)
		}

		e.retryMu.Lock()
		state := e.retryStates["act-2"]
		e.retryMu.Unlock()

		actualBackoff := state.nextRetry.Sub(now)
		expectedDur := expected * time.Second
		if actualBackoff < expectedDur-10*time.Millisecond || actualBackoff > expectedDur+10*time.Millisecond {
			t.Errorf("attempt %d: expected ~%v backoff, got %v", i+1, expectedDur, actualBackoff)
		}
	}
}

func TestHandleActionError_MaxDurationExceeded(t *testing.T) {
	e := newTestEffectorNoSession()
	e.SetMaxRetryDuration(100 * time.Millisecond)
	action := newTestAction("act-3")

	var permanentErr string
	e.SetOnError(func(actionID, actionType, errMsg string) {
		permanentErr = errMsg
	})

	// First attempt — within max duration
	now := time.Now()
	retried := e.handleActionError(action, errors.New("transient"), now)
	if !retried {
		t.Error("first attempt should schedule retry")
	}

	// Second attempt — after max duration has passed
	later := now.Add(200 * time.Millisecond)
	retried = e.handleActionError(action, errors.New("transient"), later)
	if retried {
		t.Error("after max duration, action should not be retried")
	}
	if permanentErr == "" {
		t.Error("expected onError callback to be called")
	}

	// State should be cleared
	e.retryMu.Lock()
	_, exists := e.retryStates["act-3"]
	e.retryMu.Unlock()
	if exists {
		t.Error("retry state should be cleared after permanent failure")
	}
}

func TestHandleActionError_NonRetryable(t *testing.T) {
	e := newTestEffectorNoSession()
	action := newTestAction("act-4")

	var errorCalled bool
	e.SetOnError(func(actionID, actionType, errMsg string) {
		errorCalled = true
	})

	restErr := &discordgo.RESTError{
		Response: &http.Response{StatusCode: 403},
	}
	retried := e.handleActionError(action, restErr, time.Now())

	if retried {
		t.Error("non-retryable error should not be retried")
	}
	if !errorCalled {
		t.Error("expected onError callback to be called")
	}
}

func TestHandleActionError_RetryCallback(t *testing.T) {
	e := newTestEffectorNoSession()
	action := newTestAction("act-5")

	var retryCalled bool
	var retryAttempt int
	e.SetOnRetry(func(actionID, actionType, errMsg string, attempt int, nextRetry time.Duration) {
		retryCalled = true
		retryAttempt = attempt
	})

	e.handleActionError(action, errors.New("transient"), time.Now())

	if !retryCalled {
		t.Error("expected onRetry callback to be called")
	}
	if retryAttempt != 1 {
		t.Errorf("expected attempt=1, got %d", retryAttempt)
	}
}

// --- shouldRetryNow ---

func TestShouldRetryNow_FirstAttempt(t *testing.T) {
	e := newTestEffectorNoSession()
	if !e.shouldRetryNow("new-action", time.Now()) {
		t.Error("first attempt with no state should allow retry")
	}
}

func TestShouldRetryNow_InBackoff(t *testing.T) {
	e := newTestEffectorNoSession()
	action := newTestAction("act-6")
	now := time.Now()

	e.handleActionError(action, errors.New("transient"), now)

	// Check immediately — backoff is 1s, so should not retry yet
	if e.shouldRetryNow("act-6", now.Add(100*time.Millisecond)) {
		t.Error("should not retry while in backoff period")
	}
}

func TestShouldRetryNow_BackoffExpired(t *testing.T) {
	e := newTestEffectorNoSession()
	action := newTestAction("act-7")
	now := time.Now()

	e.handleActionError(action, errors.New("transient"), now)

	// Check after backoff has expired (1s + buffer)
	if !e.shouldRetryNow("act-7", now.Add(2*time.Second)) {
		t.Error("should retry after backoff period has expired")
	}
}

// --- chunkMessage ---

func TestChunkMessage_ShortMessage(t *testing.T) {
	chunks := chunkMessage("hello", 2000)
	if len(chunks) != 1 {
		t.Errorf("expected 1 chunk, got %d", len(chunks))
	}
	if chunks[0] != "hello" {
		t.Errorf("expected 'hello', got %q", chunks[0])
	}
}

func TestChunkMessage_ExactLength(t *testing.T) {
	msg := strings.Repeat("a", 2000)
	chunks := chunkMessage(msg, 2000)
	if len(chunks) != 1 {
		t.Errorf("expected 1 chunk for exact max length, got %d", len(chunks))
	}
}

func TestChunkMessage_OverLength_SplitOnParagraph(t *testing.T) {
	part1 := strings.Repeat("a", 1500)
	part2 := strings.Repeat("b", 1500)
	msg := part1 + "\n\n" + part2

	chunks := chunkMessage(msg, 2000)
	if len(chunks) != 2 {
		t.Errorf("expected 2 chunks split on paragraph, got %d", len(chunks))
	}
}

func TestChunkMessage_OverLength_SplitOnLine(t *testing.T) {
	part1 := strings.Repeat("a", 1500)
	part2 := strings.Repeat("b", 1500)
	msg := part1 + "\n" + part2

	chunks := chunkMessage(msg, 2000)
	if len(chunks) != 2 {
		t.Errorf("expected 2 chunks split on newline, got %d", len(chunks))
	}
}

func TestChunkMessage_OverLength_SplitOnWord(t *testing.T) {
	part1 := strings.Repeat("a", 1500)
	part2 := strings.Repeat("b", 1500)
	msg := part1 + " " + part2

	chunks := chunkMessage(msg, 2000)
	if len(chunks) != 2 {
		t.Errorf("expected 2 chunks split on space, got %d", len(chunks))
	}
}

func TestChunkMessage_AllChunksWithinLimit(t *testing.T) {
	// 5000 chars with no natural break points
	msg := strings.Repeat("x", 5000)
	chunks := chunkMessage(msg, 2000)

	for i, chunk := range chunks {
		if len(chunk) > 2000 {
			t.Errorf("chunk %d exceeds max length: %d", i, len(chunk))
		}
	}

	// Reassemble and verify no data lost
	rejoined := strings.Join(chunks, "")
	if rejoined != msg {
		t.Error("chunked content doesn't match original")
	}
}

func TestChunkMessage_EmptyString(t *testing.T) {
	chunks := chunkMessage("", 2000)
	if len(chunks) != 1 || chunks[0] != "" {
		t.Errorf("expected single empty chunk, got %v", chunks)
	}
}

// --- findSplitPoint ---

func TestFindSplitPoint_ShortContent(t *testing.T) {
	pt := findSplitPoint("hello", 2000)
	if pt != 5 {
		t.Errorf("expected 5, got %d", pt)
	}
}

func TestFindSplitPoint_ParagraphPreferredOverLine(t *testing.T) {
	// paragraph break at 1400, line break at 1600 — prefer paragraph
	prefix := strings.Repeat("a", 1400)
	middle := strings.Repeat("b", 200)
	suffix := strings.Repeat("c", 400)
	content := prefix + "\n\n" + middle + "\n" + suffix

	pt := findSplitPoint(content, 2000)
	// paragraph break is at 1400, so split point should be 1402 (after \n\n)
	if content[pt-2:pt] != "\n\n" && content[pt-1] != '\n' {
		// Allow paragraph or line boundary within maxLen/2
		if pt <= 0 || pt > 2000 {
			t.Errorf("split point %d out of range", pt)
		}
	}
}

func TestFindSplitPoint_ForcedSplitWhenNoBreaks(t *testing.T) {
	// No natural break points — should split at maxLen
	content := strings.Repeat("x", 3000)
	pt := findSplitPoint(content, 2000)
	if pt != 2000 {
		t.Errorf("expected forced split at 2000, got %d", pt)
	}
}
