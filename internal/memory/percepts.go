package memory

import (
	"encoding/json"
	"os"
	"sync"
	"time"

	"github.com/vthunder/bud2/internal/types"
)

// PerceptPool manages the percept pool
type PerceptPool struct {
	mu       sync.RWMutex
	percepts map[string]*types.Percept
	path     string
}

// NewPerceptPool creates a new percept pool
func NewPerceptPool(path string) *PerceptPool {
	return &PerceptPool{
		percepts: make(map[string]*types.Percept),
		path:     path,
	}
}

// Add adds a percept to the pool
func (p *PerceptPool) Add(percept *types.Percept) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.percepts[percept.ID] = percept
}

// Get retrieves a percept by ID (returns nil if expired/missing)
func (p *PerceptPool) Get(id string) *types.Percept {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.percepts[id]
}

// GetMany retrieves multiple percepts by ID (skips missing/expired)
func (p *PerceptPool) GetMany(ids []string) []*types.Percept {
	p.mu.RLock()
	defer p.mu.RUnlock()

	result := make([]*types.Percept, 0, len(ids))
	for _, id := range ids {
		if percept, ok := p.percepts[id]; ok {
			result = append(result, percept)
		}
	}
	return result
}

// ExpireOlderThan removes percepts older than maxAge
func (p *PerceptPool) ExpireOlderThan(maxAge time.Duration) int {
	p.mu.Lock()
	defer p.mu.Unlock()

	cutoff := time.Now().Add(-maxAge)
	expired := 0
	for id, percept := range p.percepts {
		if percept.Timestamp.Before(cutoff) {
			delete(p.percepts, id)
			expired++
		}
	}
	return expired
}

// All returns all percepts (for iteration)
func (p *PerceptPool) All() []*types.Percept {
	p.mu.RLock()
	defer p.mu.RUnlock()

	result := make([]*types.Percept, 0, len(p.percepts))
	for _, percept := range p.percepts {
		result = append(result, percept)
	}
	return result
}

// Load reads percepts from disk
func (p *PerceptPool) Load() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	data, err := os.ReadFile(p.path)
	if os.IsNotExist(err) {
		return nil // empty pool is fine
	}
	if err != nil {
		return err
	}

	var file struct {
		Percepts []*types.Percept `json:"percepts"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return err
	}

	p.percepts = make(map[string]*types.Percept)
	for _, percept := range file.Percepts {
		p.percepts[percept.ID] = percept
	}
	return nil
}

// Save writes percepts to disk
func (p *PerceptPool) Save() error {
	p.mu.RLock()
	defer p.mu.RUnlock()

	file := struct {
		Percepts []*types.Percept `json:"percepts"`
	}{
		Percepts: make([]*types.Percept, 0, len(p.percepts)),
	}
	for _, percept := range p.percepts {
		file.Percepts = append(file.Percepts, percept)
	}

	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p.path, data, 0644)
}

// Clear removes all percepts, returns count deleted
func (p *PerceptPool) Clear() int {
	p.mu.Lock()
	defer p.mu.Unlock()

	count := len(p.percepts)
	p.percepts = make(map[string]*types.Percept)
	return count
}

// Count returns the number of percepts
func (p *PerceptPool) Count() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.percepts)
}
