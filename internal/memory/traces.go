package memory

import (
	"encoding/json"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/vthunder/bud2/internal/embedding"
	"github.com/vthunder/bud2/internal/types"
)

// TracePool manages consolidated memory traces
type TracePool struct {
	mu     sync.RWMutex
	traces map[string]*types.Trace
	path   string
}

// NewTracePool creates a new trace pool
func NewTracePool(path string) *TracePool {
	return &TracePool{
		traces: make(map[string]*types.Trace),
		path:   path,
	}
}

// Add adds a trace to the pool
func (p *TracePool) Add(trace *types.Trace) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.traces[trace.ID] = trace
}

// Get retrieves a trace by ID
func (p *TracePool) Get(id string) *types.Trace {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.traces[id]
}

// All returns all traces
func (p *TracePool) All() []*types.Trace {
	p.mu.RLock()
	defer p.mu.RUnlock()

	result := make([]*types.Trace, 0, len(p.traces))
	for _, trace := range p.traces {
		result = append(result, trace)
	}
	return result
}

// DecayActivation reduces activation levels over time
func (p *TracePool) DecayActivation(decayRate float64) {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, trace := range p.traces {
		trace.Activation *= decayRate
	}
}

// SpreadActivation boosts traces similar to the given embedding
// Returns the traces that were activated above threshold
func (p *TracePool) SpreadActivation(emb []float64, boost float64, threshold float64) []*types.Trace {
	if len(emb) == 0 {
		return nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	var activated []*types.Trace
	for _, trace := range p.traces {
		if len(trace.Embedding) == 0 {
			continue
		}

		similarity := embedding.CosineSimilarity(emb, trace.Embedding)
		if similarity > threshold {
			// Boost proportional to similarity
			trace.Activation += boost * similarity
			if trace.Activation > 1.0 {
				trace.Activation = 1.0
			}
			trace.LastAccess = time.Now()
			activated = append(activated, trace)
		}
	}

	return activated
}

// GetActivated returns traces above activation threshold, sorted by activation
func (p *TracePool) GetActivated(threshold float64, limit int) []*types.Trace {
	p.mu.RLock()
	defer p.mu.RUnlock()

	var result []*types.Trace
	for _, trace := range p.traces {
		if trace.Activation >= threshold {
			result = append(result, trace)
		}
	}

	// Sort by activation descending
	sort.Slice(result, func(i, j int) bool {
		return result[i].Activation > result[j].Activation
	})

	if limit > 0 && len(result) > limit {
		result = result[:limit]
	}

	return result
}

// FindSimilar finds traces similar to the given embedding
func (p *TracePool) FindSimilar(emb []float64, threshold float64) []*types.Trace {
	if len(emb) == 0 {
		return nil
	}

	p.mu.RLock()
	defer p.mu.RUnlock()

	var result []*types.Trace
	for _, trace := range p.traces {
		if len(trace.Embedding) == 0 {
			continue
		}
		if embedding.CosineSimilarity(emb, trace.Embedding) >= threshold {
			result = append(result, trace)
		}
	}
	return result
}

// Reinforce strengthens an existing trace (called when similar content arrives)
func (p *TracePool) Reinforce(id string, boost float64) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if trace, ok := p.traces[id]; ok {
		trace.Strength++
		trace.Activation += boost
		if trace.Activation > 1.0 {
			trace.Activation = 1.0
		}
		trace.LastAccess = time.Now()
	}
}

// PruneWeak removes traces that are weak and old
func (p *TracePool) PruneWeak(minStrength int, maxAge time.Duration) int {
	p.mu.Lock()
	defer p.mu.Unlock()

	cutoff := time.Now().Add(-maxAge)
	pruned := 0
	for id, trace := range p.traces {
		if trace.Strength < minStrength && trace.LastAccess.Before(cutoff) {
			delete(p.traces, id)
			pruned++
		}
	}
	return pruned
}

// Load reads traces from disk
func (p *TracePool) Load() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	data, err := os.ReadFile(p.path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}

	var file struct {
		Traces []*types.Trace `json:"traces"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return err
	}

	p.traces = make(map[string]*types.Trace)
	for _, trace := range file.Traces {
		p.traces[trace.ID] = trace
	}
	return nil
}

// Save writes traces to disk
func (p *TracePool) Save() error {
	p.mu.RLock()
	defer p.mu.RUnlock()

	file := struct {
		Traces []*types.Trace `json:"traces"`
	}{
		Traces: make([]*types.Trace, 0, len(p.traces)),
	}
	for _, trace := range p.traces {
		file.Traces = append(file.Traces, trace)
	}

	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p.path, data, 0644)
}

// Count returns the number of traces
func (p *TracePool) Count() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.traces)
}

// HasSource checks if any trace contains the given source ID
func (p *TracePool) HasSource(sourceID string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()

	for _, trace := range p.traces {
		for _, src := range trace.Sources {
			if src == sourceID {
				return true
			}
		}
	}
	return false
}

// GetCore returns all core identity traces
func (p *TracePool) GetCore() []*types.Trace {
	p.mu.RLock()
	defer p.mu.RUnlock()

	var result []*types.Trace
	for _, trace := range p.traces {
		if trace.IsCore {
			result = append(result, trace)
		}
	}
	return result
}

// HasCore returns true if any core traces exist
func (p *TracePool) HasCore() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()

	for _, trace := range p.traces {
		if trace.IsCore {
			return true
		}
	}
	return false
}

// SetCore marks a trace as core (or removes core status)
func (p *TracePool) SetCore(id string, isCore bool) bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	if trace, ok := p.traces[id]; ok {
		trace.IsCore = isCore
		return true
	}
	return false
}

// Delete removes a trace by ID, returns true if found
func (p *TracePool) Delete(id string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	if _, ok := p.traces[id]; ok {
		delete(p.traces, id)
		return true
	}
	return false
}

// ClearNonCore removes all non-core traces, returns count deleted
func (p *TracePool) ClearNonCore() int {
	p.mu.Lock()
	defer p.mu.Unlock()

	cleared := 0
	for id, trace := range p.traces {
		if !trace.IsCore {
			delete(p.traces, id)
			cleared++
		}
	}
	return cleared
}

// ClearCore removes all core traces, returns count deleted
func (p *TracePool) ClearCore() int {
	p.mu.Lock()
	defer p.mu.Unlock()

	cleared := 0
	for id, trace := range p.traces {
		if trace.IsCore {
			delete(p.traces, id)
			cleared++
		}
	}
	return cleared
}
