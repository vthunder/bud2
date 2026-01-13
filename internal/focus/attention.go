package focus

import (
	"sort"
	"sync"
	"time"
)

// Attention manages the focus-based attention system
type Attention struct {
	mu       sync.RWMutex
	state    FocusState
	pending  []*PendingItem
	callback FocusCallback
}

// FocusCallback is called when focus changes
type FocusCallback func(item *PendingItem)

// New creates a new attention system
func New() *Attention {
	return &Attention{
		state: FocusState{
			Arousal: 0.3, // Default moderate arousal
		},
		pending: make([]*PendingItem, 0),
	}
}

// SetCallback sets the callback for focus changes
func (a *Attention) SetCallback(cb FocusCallback) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.callback = cb
}

// AddPending adds an item to the pending queue
func (a *Attention) AddPending(item *PendingItem) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if item.Timestamp.IsZero() {
		item.Timestamp = time.Now()
	}

	// Compute salience if not set
	if item.Salience == 0 {
		item.Salience = a.computeSalience(item)
	}

	a.pending = append(a.pending, item)

	// Adjust arousal based on input
	a.adjustArousal(item)
}

// SelectNext selects the next item to focus on
func (a *Attention) SelectNext() *PendingItem {
	a.mu.Lock()
	defer a.mu.Unlock()

	if len(a.pending) == 0 {
		return nil
	}

	// Sort by priority then salience
	sort.Slice(a.pending, func(i, j int) bool {
		if a.pending[i].Priority != a.pending[j].Priority {
			return a.pending[i].Priority < a.pending[j].Priority
		}
		return a.pending[i].Salience > a.pending[j].Salience
	})

	// P0 always wins
	if a.pending[0].Priority == P0Critical {
		return a.selectItem(0)
	}

	// User input wins unless P0
	for i, item := range a.pending {
		if item.Type == "user_input" {
			return a.selectItem(i)
		}
	}

	// Otherwise, check threshold based on arousal
	threshold := a.getSelectionThreshold()
	if a.pending[0].Salience >= threshold {
		return a.selectItem(0)
	}

	return nil // Nothing meets threshold
}

// selectItem removes item from pending and returns it
func (a *Attention) selectItem(index int) *PendingItem {
	item := a.pending[index]
	a.pending = append(a.pending[:index], a.pending[index+1:]...)
	return item
}

// Focus sets the current focus item
func (a *Attention) Focus(item *PendingItem) {
	a.mu.Lock()

	// Save current to suspended if exists
	if a.state.CurrentItem != nil {
		a.state.Suspended = append([]*PendingItem{a.state.CurrentItem}, a.state.Suspended...)
	}

	a.state.CurrentItem = item
	callback := a.callback
	a.mu.Unlock()

	// Notify callback (outside lock)
	if callback != nil && item != nil {
		callback(item)
	}
}

// Complete marks current focus as complete
func (a *Attention) Complete() {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.state.CurrentItem = nil

	// Resume suspended if any
	if len(a.state.Suspended) > 0 {
		a.state.CurrentItem = a.state.Suspended[0]
		a.state.Suspended = a.state.Suspended[1:]
	}
}

// GetCurrent returns the current focus item
func (a *Attention) GetCurrent() *PendingItem {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.state.CurrentItem
}

// GetState returns a copy of the current state
func (a *Attention) GetState() FocusState {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.state
}

// GetPendingCount returns the number of pending items
func (a *Attention) GetPendingCount() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return len(a.pending)
}

// SetMode sets an attention mode for a domain
func (a *Attention) SetMode(mode *Mode) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Remove existing mode for this domain
	var filtered []*Mode
	for _, m := range a.state.Modes {
		if m.Domain != mode.Domain {
			filtered = append(filtered, m)
		}
	}
	a.state.Modes = append(filtered, mode)
}

// ClearMode removes an attention mode for a domain
func (a *Attention) ClearMode(domain string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	var filtered []*Mode
	for _, m := range a.state.Modes {
		if m.Domain != domain {
			filtered = append(filtered, m)
		}
	}
	a.state.Modes = filtered
}

// IsAttending returns true if we're attending to the given domain
func (a *Attention) IsAttending(domain string) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()

	for _, m := range a.state.Modes {
		if m.IsExpired() {
			continue
		}
		if m.Domain == domain || m.Domain == "all" {
			return true
		}
	}
	return false
}

// GetActiveMode returns the active mode for a domain (if any)
func (a *Attention) GetActiveMode(domain string) *Mode {
	a.mu.RLock()
	defer a.mu.RUnlock()

	for _, m := range a.state.Modes {
		if m.IsExpired() {
			continue
		}
		if m.Domain == domain || m.Domain == "all" {
			return m
		}
	}
	return nil
}

// CleanExpiredModes removes expired modes
func (a *Attention) CleanExpiredModes() {
	a.mu.Lock()
	defer a.mu.Unlock()

	var active []*Mode
	for _, m := range a.state.Modes {
		if !m.IsExpired() {
			active = append(active, m)
		}
	}
	a.state.Modes = active
}

// computeSalience computes salience for an item
func (a *Attention) computeSalience(item *PendingItem) float64 {
	base := 0.5

	// Priority boost
	switch item.Priority {
	case P0Critical:
		base = 1.0
	case P1UserInput:
		base = 0.9
	case P2DueTask:
		base = 0.7
	case P3ActiveWork:
		base = 0.5
	case P4Exploration:
		base = 0.2
	}

	// Source boost
	if item.Source == "discord" {
		base += 0.1
	}

	// Recency boost (items less than 1 minute old get boost)
	age := time.Since(item.Timestamp).Seconds()
	if age < 60 {
		base += 0.1 * (1 - age/60)
	}

	// Cap at 1.0
	if base > 1.0 {
		base = 1.0
	}

	return base
}

// adjustArousal adjusts overall arousal based on input
func (a *Attention) adjustArousal(item *PendingItem) {
	// High priority items increase arousal
	if item.Priority <= P1UserInput {
		a.state.Arousal = min(a.state.Arousal+0.1, 1.0)
	}

	// Over time, arousal decays (this would be called periodically)
}

// DecayArousal reduces arousal over time
func (a *Attention) DecayArousal(factor float64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state.Arousal *= factor
	if a.state.Arousal < 0.1 {
		a.state.Arousal = 0.1 // Minimum arousal
	}
}

// getSelectionThreshold returns the salience threshold for selection
func (a *Attention) getSelectionThreshold() float64 {
	// Higher arousal = lower threshold (more responsive)
	// Base threshold: 0.6, can go down to 0.3 at max arousal
	return 0.6 - (a.state.Arousal * 0.3)
}

func min(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
