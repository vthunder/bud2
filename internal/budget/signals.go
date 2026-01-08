package budget

import (
	"bufio"
	"encoding/json"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Signal represents a signal from the MCP server
type Signal struct {
	Type      string    `json:"type"`
	SessionID string    `json:"session_id,omitempty"`
	Summary   string    `json:"summary,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// SignalProcessor reads signals from signals.jsonl and processes them
type SignalProcessor struct {
	statePath  string
	tracker    *SessionTracker
	offset     int64
	mu         sync.Mutex
	onComplete func(session *Session, summary string) // called when session completes
}

// NewSignalProcessor creates a new processor
func NewSignalProcessor(statePath string, tracker *SessionTracker) *SignalProcessor {
	return &SignalProcessor{
		statePath: statePath,
		tracker:   tracker,
	}
}

// SetOnComplete sets the callback for session completion
func (p *SignalProcessor) SetOnComplete(cb func(session *Session, summary string)) {
	p.onComplete = cb
}

// ProcessSignals reads new signals from the file and processes them
// Returns the number of signals processed
func (p *SignalProcessor) ProcessSignals() int {
	p.mu.Lock()
	defer p.mu.Unlock()

	signalsPath := filepath.Join(p.statePath, "queues", "signals.jsonl")

	f, err := os.Open(signalsPath)
	if err != nil {
		return 0 // File doesn't exist yet
	}
	defer f.Close()

	// Seek to last position
	if p.offset > 0 {
		f.Seek(p.offset, io.SeekStart)
	}

	scanner := bufio.NewScanner(f)
	processed := 0

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		var signal Signal
		if err := json.Unmarshal([]byte(line), &signal); err != nil {
			log.Printf("[signals] Failed to parse signal: %v", err)
			continue
		}

		p.handleSignal(signal)
		processed++
	}

	// Update offset
	newOffset, _ := f.Seek(0, io.SeekCurrent)
	p.offset = newOffset

	return processed
}

func (p *SignalProcessor) handleSignal(signal Signal) {
	switch signal.Type {
	case "session_done":
		if p.tracker != nil {
			session := p.tracker.CompleteSession(signal.SessionID)
			if session != nil && session.DurationSec > 0 {
				log.Printf("[signals] Session %s completed in %.1f seconds: %s",
					signal.SessionID, session.DurationSec, signal.Summary)
				// Call completion callback
				if p.onComplete != nil {
					p.onComplete(session, signal.Summary)
				}
			} else {
				log.Printf("[signals] Session done signal received (unknown start): %s", signal.Summary)
			}
		}
	default:
		log.Printf("[signals] Unknown signal type: %s", signal.Type)
	}
}

// Start begins polling for signals in the background
func (p *SignalProcessor) Start(interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for range ticker.C {
			p.ProcessSignals()
		}
	}()
}
