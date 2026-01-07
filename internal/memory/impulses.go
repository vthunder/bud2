package memory

import (
	"encoding/json"
	"os"
	"sync"
	"time"

	"github.com/vthunder/bud2/internal/types"
)

// ImpulsePool manages internal motivation impulses
type ImpulsePool struct {
	path     string
	impulses map[string]*types.Impulse
	mu       sync.RWMutex
}

// NewImpulsePool creates a new impulse pool
func NewImpulsePool(path string) *ImpulsePool {
	return &ImpulsePool{
		path:     path,
		impulses: make(map[string]*types.Impulse),
	}
}

// Add adds an impulse to the pool
func (p *ImpulsePool) Add(impulse *types.Impulse) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.impulses[impulse.ID] = impulse
}

// Get retrieves an impulse by ID
func (p *ImpulsePool) Get(id string) *types.Impulse {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.impulses[id]
}

// Remove removes an impulse (after it's been processed)
func (p *ImpulsePool) Remove(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.impulses, id)
}

// GetAll returns all current impulses
func (p *ImpulsePool) GetAll() []*types.Impulse {
	p.mu.RLock()
	defer p.mu.RUnlock()

	result := make([]*types.Impulse, 0, len(p.impulses))
	for _, imp := range p.impulses {
		result = append(result, imp)
	}
	return result
}

// GetPending returns impulses that haven't been processed yet
// (intensity > 0 means not yet handled)
func (p *ImpulsePool) GetPending() []*types.Impulse {
	p.mu.RLock()
	defer p.mu.RUnlock()

	var result []*types.Impulse
	for _, imp := range p.impulses {
		if imp.Intensity > 0 {
			result = append(result, imp)
		}
	}
	return result
}

// Expire removes impulses older than the given duration
func (p *ImpulsePool) Expire(maxAge time.Duration) int {
	p.mu.Lock()
	defer p.mu.Unlock()

	cutoff := time.Now().Add(-maxAge)
	count := 0
	for id, imp := range p.impulses {
		if imp.Timestamp.Before(cutoff) {
			delete(p.impulses, id)
			count++
		}
	}
	return count
}

// Count returns the number of impulses
func (p *ImpulsePool) Count() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.impulses)
}

// Load loads impulses from file
func (p *ImpulsePool) Load() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	data, err := os.ReadFile(p.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var file struct {
		Impulses []*types.Impulse `json:"impulses"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return err
	}

	p.impulses = make(map[string]*types.Impulse)
	for _, imp := range file.Impulses {
		p.impulses[imp.ID] = imp
	}
	return nil
}

// Save persists impulses to file
func (p *ImpulsePool) Save() error {
	p.mu.RLock()
	defer p.mu.RUnlock()

	impulses := make([]*types.Impulse, 0, len(p.impulses))
	for _, imp := range p.impulses {
		impulses = append(impulses, imp)
	}

	file := struct {
		Impulses []*types.Impulse `json:"impulses"`
	}{
		Impulses: impulses,
	}

	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p.path, data, 0644)
}
