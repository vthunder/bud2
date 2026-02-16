package profiling

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// ProfilingLevel determines how detailed the profiling is
type ProfilingLevel string

const (
	LevelOff      ProfilingLevel = "off"      // No profiling
	LevelMinimal  ProfilingLevel = "minimal"  // L1: Key stages only
	LevelDetailed ProfilingLevel = "detailed" // L2: Substages included
	LevelTrace    ProfilingLevel = "trace"    // L3: Every function (future)
)

// MessageTiming represents a single timing measurement
type MessageTiming struct {
	MessageID  string                 `json:"message_id"`
	Stage      string                 `json:"stage"`
	StartTime  time.Time              `json:"start_time"`
	DurationMs float64                `json:"duration_ms"`
	Metadata   map[string]interface{} `json:"metadata,omitempty"`
}

// Profiler handles timing measurements for message processing
type Profiler struct {
	enabled  bool
	level    ProfilingLevel
	logPath  string
	mu       sync.Mutex
	logFile  *os.File
	encoder  *json.Encoder
}

var globalProfiler *Profiler
var once sync.Once

// Init initializes the global profiler
func Init(level ProfilingLevel, logPath string) error {
	var err error
	once.Do(func() {
		globalProfiler = &Profiler{
			enabled: level != LevelOff,
			level:   level,
			logPath: logPath,
		}

		if globalProfiler.enabled {
			err = globalProfiler.openLogFile()
		}
	})
	return err
}

// Get returns the global profiler instance
func Get() *Profiler {
	if globalProfiler == nil {
		// Default to off if not initialized
		_ = Init(LevelOff, "")
	}
	return globalProfiler
}

// openLogFile opens the log file for writing
func (p *Profiler) openLogFile() error {
	var err error
	p.logFile, err = os.OpenFile(p.logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to open profiling log: %w", err)
	}
	p.encoder = json.NewEncoder(p.logFile)
	return nil
}

// Close closes the profiler and its log file
func (p *Profiler) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.logFile != nil {
		return p.logFile.Close()
	}
	return nil
}

// Start begins timing a stage and returns a function to call when done
func (p *Profiler) Start(messageID, stage string) func() {
	if !p.enabled {
		return func() {}
	}

	start := time.Now()
	return func() {
		p.Record(messageID, stage, time.Since(start), nil)
	}
}

// StartWithMetadata begins timing a stage with additional metadata
func (p *Profiler) StartWithMetadata(messageID, stage string, metadata map[string]interface{}) func() {
	if !p.enabled {
		return func() {}
	}

	start := time.Now()
	return func() {
		p.Record(messageID, stage, time.Since(start), metadata)
	}
}

// Record records a timing measurement
func (p *Profiler) Record(messageID, stage string, duration time.Duration, metadata map[string]interface{}) {
	if !p.enabled {
		return
	}

	timing := MessageTiming{
		MessageID:  messageID,
		Stage:      stage,
		StartTime:  time.Now().Add(-duration),
		DurationMs: float64(duration.Nanoseconds()) / 1e6, // Convert nanoseconds to milliseconds
		Metadata:   metadata,
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.encoder != nil {
		_ = p.encoder.Encode(timing)
	}
}

// ShouldProfile returns true if the given level should be profiled
func (p *Profiler) ShouldProfile(level ProfilingLevel) bool {
	if !p.enabled {
		return false
	}

	switch p.level {
	case LevelTrace:
		return true // Profile everything
	case LevelDetailed:
		return level == LevelMinimal || level == LevelDetailed
	case LevelMinimal:
		return level == LevelMinimal
	default:
		return false
	}
}

// IsEnabled returns true if profiling is enabled
func (p *Profiler) IsEnabled() bool {
	return p.enabled
}

// GetLevel returns the current profiling level
func (p *Profiler) GetLevel() ProfilingLevel {
	return p.level
}
