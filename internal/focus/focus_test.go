package focus

import (
	"testing"
	"time"
)

// TestPriorityPreemption tests that higher priority items preempt lower priority ones
func TestPriorityPreemption(t *testing.T) {
	attention := New()

	// Add a P3 (active work) item first
	lowPriority := &PendingItem{
		ID:        "task-1",
		Type:      "active_work",
		Priority:  P3ActiveWork,
		Content:   "Continue working on feature",
		Timestamp: time.Now(),
	}
	attention.AddPending(lowPriority)

	// Add a P1 (user input) item
	highPriority := &PendingItem{
		ID:        "msg-1",
		Type:      "user_input",
		Priority:  P1UserInput,
		Content:   "Can you help me?",
		Timestamp: time.Now(),
	}
	attention.AddPending(highPriority)

	// SelectNext should return the higher priority item
	selected := attention.SelectNext()
	if selected == nil {
		t.Fatal("Expected to select an item")
	}

	if selected.ID != "msg-1" {
		t.Errorf("Expected high priority item msg-1, got %s", selected.ID)
	}

	// The low priority item should still be pending
	if attention.GetPendingCount() != 1 {
		t.Errorf("Expected 1 pending item, got %d", attention.GetPendingCount())
	}
}

// TestP0AlwaysWins tests that P0 (critical) items always preempt everything
func TestP0AlwaysWins(t *testing.T) {
	attention := New()

	// Add multiple items
	attention.AddPending(&PendingItem{
		ID:       "task-1",
		Type:     "active_work",
		Priority: P3ActiveWork,
		Content:  "Active work",
	})
	attention.AddPending(&PendingItem{
		ID:       "msg-1",
		Type:     "user_input",
		Priority: P1UserInput,
		Content:  "User message",
	})

	// Add a P0 critical item (alarm/reminder)
	attention.AddPending(&PendingItem{
		ID:       "alarm-1",
		Type:     "reminder",
		Priority: P0Critical,
		Content:  "Meeting in 5 minutes!",
	})

	// P0 should be selected first
	selected := attention.SelectNext()
	if selected == nil || selected.Priority != P0Critical {
		t.Error("Expected P0 critical item to be selected first")
	}
}

// TestFocusSuspension tests that focus can be suspended and resumed
func TestFocusSuspension(t *testing.T) {
	attention := New()

	// Focus on a task
	task1 := &PendingItem{
		ID:      "task-1",
		Content: "Working on feature",
	}
	attention.Focus(task1)

	// Verify current focus
	current := attention.GetCurrent()
	if current == nil || current.ID != "task-1" {
		t.Error("Expected task-1 to be current focus")
	}

	// Interrupt with higher priority
	interrupt := &PendingItem{
		ID:      "msg-1",
		Content: "Urgent message",
	}
	attention.Focus(interrupt)

	// msg-1 should be current, task-1 should be suspended
	current = attention.GetCurrent()
	if current == nil || current.ID != "msg-1" {
		t.Error("Expected msg-1 to be current focus after interrupt")
	}

	state := attention.GetState()
	if len(state.Suspended) != 1 || state.Suspended[0].ID != "task-1" {
		t.Error("Expected task-1 to be in suspended stack")
	}

	// Complete the interrupt
	attention.Complete()

	// task-1 should be automatically resumed
	current = attention.GetCurrent()
	if current == nil || current.ID != "task-1" {
		t.Error("Expected task-1 to be resumed after completing interrupt")
	}
}

// TestSalienceComputation tests that salience is computed correctly
func TestSalienceComputation(t *testing.T) {
	attention := New()

	// P0 should get salience 1.0
	p0 := &PendingItem{ID: "p0", Priority: P0Critical}
	attention.AddPending(p0)

	state := attention.GetState()
	if state.Arousal == 0 {
		// Adding items should affect arousal
	}

	// Check that different priorities get appropriate salience
	tests := []struct {
		priority        Priority
		expectedMinBase float64
	}{
		{P0Critical, 0.9},
		{P1UserInput, 0.8},
		{P2DueTask, 0.6},
		{P3ActiveWork, 0.4},
		{P4Exploration, 0.1},
	}

	for _, tt := range tests {
		attention := New() // Fresh attention for each test
		item := &PendingItem{ID: "test", Priority: tt.priority}
		attention.AddPending(item)

		// The item's salience should be at least the expected base
		if item.Salience < tt.expectedMinBase {
			t.Errorf("Priority %v: expected salience >= %f, got %f",
				tt.priority, tt.expectedMinBase, item.Salience)
		}
	}
}

// TestArousalAffectsThreshold tests that arousal affects selection threshold
func TestArousalAffectsThreshold(t *testing.T) {
	// High arousal = lower threshold = more responsive
	attention := New()
	attention.state.Arousal = 0.9 // High arousal

	// Add a medium-priority item
	item := &PendingItem{
		ID:       "task-1",
		Type:     "due_task",
		Priority: P2DueTask,
		Salience: 0.4, // Below normal threshold, but should pass with high arousal
	}
	attention.pending = append(attention.pending, item)

	// With high arousal, threshold should be low enough to select
	selected := attention.SelectNext()
	if selected == nil {
		t.Error("Expected item to be selected with high arousal lowering threshold")
	}
}

// TestUserInputAlwaysSelected tests that user_input type always gets selected
func TestUserInputAlwaysSelected(t *testing.T) {
	attention := New()
	attention.state.Arousal = 0.1 // Low arousal

	// Add a user input with low computed salience
	attention.AddPending(&PendingItem{
		ID:       "msg-1",
		Type:     "user_input",
		Priority: P1UserInput,
		Content:  "hello",
	})

	// User input should always be selected regardless of threshold
	selected := attention.SelectNext()
	if selected == nil {
		t.Fatal("Expected user_input to be selected")
	}

	if selected.Type != "user_input" {
		t.Error("Expected user_input type to be selected")
	}
}

// TestAttentionModes tests the mode system
func TestAttentionModes(t *testing.T) {
	attention := New()

	// Set a mode for GTD domain
	mode := &Mode{
		Domain:    "gtd",
		Action:    "bypass_reflex",
		SetBy:     "executive",
		ExpiresAt: time.Now().Add(1 * time.Hour),
	}
	attention.SetMode(mode)

	// Check if we're attending to GTD
	if !attention.IsAttending("gtd") {
		t.Error("Expected to be attending to GTD domain")
	}

	// Check we're not attending to unset domain
	if attention.IsAttending("calendar") {
		t.Error("Expected to not be attending to calendar domain")
	}

	// Get active mode
	activeMode := attention.GetActiveMode("gtd")
	if activeMode == nil || activeMode.Action != "bypass_reflex" {
		t.Error("Expected to get the active GTD mode")
	}

	// Clear the mode
	attention.ClearMode("gtd")
	if attention.IsAttending("gtd") {
		t.Error("Expected to not be attending to GTD after clearing mode")
	}
}

// TestModeExpiration tests that modes expire correctly
func TestModeExpiration(t *testing.T) {
	attention := New()

	// Set an already-expired mode
	expiredMode := &Mode{
		Domain:    "debug",
		Action:    "verbose",
		ExpiresAt: time.Now().Add(-1 * time.Hour), // Already expired
	}
	attention.SetMode(expiredMode)

	// Should not be attending to expired mode
	if attention.IsAttending("debug") {
		t.Error("Expected expired mode to not be active")
	}

	// Clean expired modes
	attention.CleanExpiredModes()

	// Mode should be removed
	if attention.GetActiveMode("debug") != nil {
		t.Error("Expected expired mode to be cleaned up")
	}
}

// TestArousalDecay tests arousal decay over time
func TestArousalDecay(t *testing.T) {
	attention := New()
	attention.state.Arousal = 0.8

	// Decay by 0.9 (lose 10%)
	attention.DecayArousal(0.9)

	// Use approximate comparison for floating point
	expectedArousal := 0.72
	if attention.state.Arousal < expectedArousal-0.001 || attention.state.Arousal > expectedArousal+0.001 {
		t.Errorf("Expected arousal ~0.72, got %f", attention.state.Arousal)
	}

	// Decay should not go below minimum
	for i := 0; i < 100; i++ {
		attention.DecayArousal(0.5)
	}

	if attention.state.Arousal < 0.1 {
		t.Errorf("Expected minimum arousal 0.1, got %f", attention.state.Arousal)
	}
}

// TestFocusCallback tests the callback mechanism
func TestFocusCallback(t *testing.T) {
	attention := New()

	callbackCalled := false
	var calledWith *PendingItem

	attention.SetCallback(func(item *PendingItem) {
		callbackCalled = true
		calledWith = item
	})

	item := &PendingItem{ID: "test-item", Content: "Test"}
	attention.Focus(item)

	if !callbackCalled {
		t.Error("Expected callback to be called")
	}

	if calledWith == nil || calledWith.ID != "test-item" {
		t.Error("Expected callback to receive the focused item")
	}
}

// TestPriorityString tests priority string representation
func TestPriorityString(t *testing.T) {
	tests := []struct {
		priority Priority
		expected string
	}{
		{P0Critical, "P0:Critical"},
		{P1UserInput, "P1:UserInput"},
		{P2DueTask, "P2:DueTask"},
		{P3ActiveWork, "P3:ActiveWork"},
		{P4Exploration, "P4:Exploration"},
		{Priority(99), "Unknown"},
	}

	for _, tt := range tests {
		result := tt.priority.String()
		if result != tt.expected {
			t.Errorf("Priority(%d).String() = %q, want %q", tt.priority, result, tt.expected)
		}
	}
}

// TestSourceBoost tests that Discord source gets priority boost
func TestSourceBoost(t *testing.T) {
	attention := New()

	// Item from Discord
	discordItem := &PendingItem{
		ID:       "discord-msg",
		Source:   "discord",
		Priority: P2DueTask,
	}
	attention.AddPending(discordItem)

	// Item from internal source with same priority
	internalItem := &PendingItem{
		ID:       "internal-task",
		Source:   "internal",
		Priority: P2DueTask,
	}
	attention2 := New()
	attention2.AddPending(internalItem)

	// Discord item should have slightly higher salience
	if discordItem.Salience <= internalItem.Salience {
		t.Error("Expected Discord source to boost salience")
	}
}

// TestRecencyBoost tests that recent items get priority boost
func TestRecencyBoost(t *testing.T) {
	attention := New()

	// Recent item
	recentItem := &PendingItem{
		ID:        "recent",
		Priority:  P2DueTask,
		Timestamp: time.Now(),
	}
	attention.AddPending(recentItem)

	// Old item (2 minutes ago)
	attention2 := New()
	oldItem := &PendingItem{
		ID:        "old",
		Priority:  P2DueTask,
		Timestamp: time.Now().Add(-2 * time.Minute),
	}
	attention2.AddPending(oldItem)

	// Recent item should have higher salience
	if recentItem.Salience <= oldItem.Salience {
		t.Errorf("Expected recent item salience (%f) > old item salience (%f)",
			recentItem.Salience, oldItem.Salience)
	}
}
