package budget

import (
	"log"
	"strings"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v3/process"
)

// CPUWatcher monitors Claude processes by CPU usage to detect session completion.
// This serves as a fallback for signal_done - if Claude forgets to call the MCP tool,
// we can still detect when it goes idle.
type CPUWatcher struct {
	tracker *SessionTracker
	mu      sync.Mutex

	// Configuration
	pollInterval    time.Duration // How often to check CPU (default 2s)
	idleThreshold   float64       // CPU % below which we consider idle (default 3%)
	activeThreshold float64       // CPU % above which we consider active (default 30%)
	idleDuration    time.Duration // How long idle before marking complete (default 10s)

	// State per Claude process
	processes map[int32]*claudeProcessState

	// Control
	stopChan chan struct{}
	running  bool
}

type claudeProcessState struct {
	pid           int32
	sessionID     string // matched session ID (if any)
	cpuHistory    []float64
	lastActive    time.Time // last time CPU was above idle threshold
	idleSince     time.Time // when it went idle
	status        string    // "unknown", "active", "idle", "completed"
	completedOnce bool      // prevent duplicate completion signals
}

// NewCPUWatcher creates a new CPU-based session watcher
func NewCPUWatcher(tracker *SessionTracker) *CPUWatcher {
	return &CPUWatcher{
		tracker:         tracker,
		pollInterval:    2 * time.Second,
		idleThreshold:   3.0,
		activeThreshold: 30.0,
		idleDuration:    10 * time.Second,
		processes:       make(map[int32]*claudeProcessState),
		stopChan:        make(chan struct{}),
	}
}

// SetThresholds configures detection thresholds
func (w *CPUWatcher) SetThresholds(idle, active float64, idleDur time.Duration) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.idleThreshold = idle
	w.activeThreshold = active
	w.idleDuration = idleDur
}

// Start begins watching Claude processes
func (w *CPUWatcher) Start() {
	w.mu.Lock()
	if w.running {
		w.mu.Unlock()
		return
	}
	w.running = true
	w.stopChan = make(chan struct{})
	w.mu.Unlock()

	go w.watchLoop()
	log.Printf("[cpuwatcher] Started (poll=%v, idle<%.0f%%, active>%.0f%%, idle_dur=%v)",
		w.pollInterval, w.idleThreshold, w.activeThreshold, w.idleDuration)
}

// Stop stops watching
func (w *CPUWatcher) Stop() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.running {
		close(w.stopChan)
		w.running = false
	}
}

func (w *CPUWatcher) watchLoop() {
	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-w.stopChan:
			return
		case <-ticker.C:
			w.poll()
		}
	}
}

func (w *CPUWatcher) poll() {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Find all Claude processes
	claudeProcs := w.findClaudeProcesses()

	// Update state for each process
	now := time.Now()
	seenPIDs := make(map[int32]bool)

	for _, proc := range claudeProcs {
		pid := proc.Pid
		seenPIDs[pid] = true

		// Get or create state
		state, exists := w.processes[pid]
		if !exists {
			state = &claudeProcessState{
				pid:        pid,
				cpuHistory: make([]float64, 0, 5),
				lastActive: now,
				status:     "unknown",
			}
			w.processes[pid] = state
			log.Printf("[cpuwatcher] Discovered Claude process %d", pid)
		}

		// Get CPU usage
		cpu, err := proc.CPUPercent()
		if err != nil {
			continue
		}

		// Update history (keep last 5 readings)
		state.cpuHistory = append(state.cpuHistory, cpu)
		if len(state.cpuHistory) > 5 {
			state.cpuHistory = state.cpuHistory[1:]
		}

		// Calculate average CPU
		avgCPU := w.avgCPU(state.cpuHistory)

		// State machine
		w.updateState(state, avgCPU, now)
	}

	// Clean up processes that no longer exist
	for pid := range w.processes {
		if !seenPIDs[pid] {
			log.Printf("[cpuwatcher] Claude process %d exited", pid)
			delete(w.processes, pid)
		}
	}
}

func (w *CPUWatcher) findClaudeProcesses() []*process.Process {
	var result []*process.Process

	procs, err := process.Processes()
	if err != nil {
		return result
	}

	for _, proc := range procs {
		name, err := proc.Name()
		if err != nil {
			continue
		}

		// Check if it's a Claude process
		if strings.Contains(strings.ToLower(name), "claude") {
			result = append(result, proc)
			continue
		}

		// Also check command line
		cmdline, err := proc.Cmdline()
		if err != nil {
			continue
		}
		if strings.Contains(strings.ToLower(cmdline), "claude") {
			// Exclude our own processes
			if strings.Contains(cmdline, "bud-mcp") || strings.Contains(cmdline, "cpuwatcher") {
				continue
			}
			result = append(result, proc)
		}
	}

	return result
}

func (w *CPUWatcher) avgCPU(history []float64) float64 {
	if len(history) == 0 {
		return 0
	}
	var sum float64
	for _, v := range history {
		sum += v
	}
	return sum / float64(len(history))
}

func (w *CPUWatcher) updateState(state *claudeProcessState, avgCPU float64, now time.Time) {
	prevStatus := state.status

	switch state.status {
	case "unknown":
		// Need a few readings before deciding
		if len(state.cpuHistory) >= 3 {
			if avgCPU > w.activeThreshold {
				state.status = "active"
				state.lastActive = now
			} else {
				state.status = "idle"
				state.idleSince = now
			}
		}

	case "active":
		if avgCPU > w.idleThreshold {
			state.lastActive = now
		} else {
			// Went idle
			state.status = "idle"
			state.idleSince = now
		}

	case "idle":
		if avgCPU > w.activeThreshold {
			// Became active again
			state.status = "active"
			state.lastActive = now
			state.completedOnce = false // reset completion flag
		} else if now.Sub(state.idleSince) >= w.idleDuration {
			// Been idle long enough - mark as completed
			if !state.completedOnce {
				state.status = "completed"
				w.onSessionCompleted(state, now)
			}
		}

	case "completed":
		if avgCPU > w.activeThreshold {
			// Woke up again
			state.status = "active"
			state.lastActive = now
			state.completedOnce = false
		}
	}

	if state.status != prevStatus {
		log.Printf("[cpuwatcher] Process %d: %s → %s (avg CPU: %.1f%%)",
			state.pid, prevStatus, state.status, avgCPU)
	}
}

func (w *CPUWatcher) onSessionCompleted(state *claudeProcessState, now time.Time) {
	state.completedOnce = true

	// Try to match with an active session
	if w.tracker == nil {
		return
	}

	activeSessions := w.tracker.GetActiveSessions()
	if len(activeSessions) == 0 {
		log.Printf("[cpuwatcher] Process %d went idle but no active sessions to complete", state.pid)
		return
	}

	// Complete the oldest active session (simple heuristic)
	// In practice, if there's only one session, this works fine
	oldestSession := activeSessions[0]
	for _, s := range activeSessions[1:] {
		if s.StartedAt.Before(oldestSession.StartedAt) {
			oldestSession = s
		}
	}

	// Mark complete via tracker
	completed := w.tracker.CompleteSession(oldestSession.ID)
	if completed != nil {
		log.Printf("[cpuwatcher] Completed session %s via CPU detection (%.1f sec, fallback for signal_done)",
			oldestSession.ID, completed.DurationSec)
	}
}

// GetStatus returns current watcher status for debugging
func (w *CPUWatcher) GetStatus() map[int32]string {
	w.mu.Lock()
	defer w.mu.Unlock()

	result := make(map[int32]string)
	for pid, state := range w.processes {
		result[pid] = state.status
	}
	return result
}
