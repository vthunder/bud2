package motivation

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/vthunder/bud2/internal/types"
)

// Idea represents something to explore someday ("I want to explore X")
type Idea struct {
	ID        string    `json:"id"`
	Idea      string    `json:"idea"`
	SparkBy   string    `json:"sparked_by"` // what triggered this idea
	Added     time.Time `json:"added"`
	Priority  int       `json:"priority"`  // 1 = highest interest
	Explored  bool      `json:"explored"`  // has it been explored?
	Notes     string    `json:"notes"`     // any notes from exploration
}

// IdeaStore manages ideas.json
type IdeaStore struct {
	path  string
	ideas map[string]*Idea
	mu    sync.RWMutex
}

// NewIdeaStore creates a new idea store
func NewIdeaStore(statePath string) *IdeaStore {
	return &IdeaStore{
		path:  filepath.Join(statePath, "ideas.json"),
		ideas: make(map[string]*Idea),
	}
}

// Load reads ideas from file
func (s *IdeaStore) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var ideas []*Idea
	if err := json.Unmarshal(data, &ideas); err != nil {
		return err
	}

	s.ideas = make(map[string]*Idea)
	for _, i := range ideas {
		s.ideas[i.ID] = i
	}
	return nil
}

// Save writes ideas to file
func (s *IdeaStore) Save() error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ideas := make([]*Idea, 0, len(s.ideas))
	for _, i := range s.ideas {
		ideas = append(ideas, i)
	}

	data, err := json.MarshalIndent(ideas, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0644)
}

// Add adds a new idea
func (s *IdeaStore) Add(idea *Idea) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if idea.ID == "" {
		idea.ID = fmt.Sprintf("idea-%d", time.Now().UnixNano())
	}
	if idea.Added.IsZero() {
		idea.Added = time.Now()
	}
	s.ideas[idea.ID] = idea
}

// Get retrieves an idea by ID
func (s *IdeaStore) Get(id string) *Idea {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ideas[id]
}

// Remove removes an idea
func (s *IdeaStore) Remove(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.ideas, id)
}

// MarkExplored marks an idea as explored with notes
func (s *IdeaStore) MarkExplored(id string, notes string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if i, ok := s.ideas[id]; ok {
		i.Explored = true
		i.Notes = notes
	}
}

// GetUnexplored returns all unexplored ideas
func (s *IdeaStore) GetUnexplored() []*Idea {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []*Idea
	for _, i := range s.ideas {
		if !i.Explored {
			result = append(result, i)
		}
	}
	return result
}

// GetRandom returns a random unexplored idea for exploration
// Returns nil if no unexplored ideas exist
func (s *IdeaStore) GetRandom() *Idea {
	unexplored := s.GetUnexplored()
	if len(unexplored) == 0 {
		return nil
	}
	// Simple: just return the first one (or could randomize)
	// Prioritize by priority
	best := unexplored[0]
	for _, i := range unexplored[1:] {
		if i.Priority < best.Priority { // lower number = higher priority
			best = i
		}
	}
	return best
}

// GenerateImpulses creates impulses for idea exploration (only during idle time)
// Returns at most one impulse - the most interesting unexplored idea
func (s *IdeaStore) GenerateImpulses(isIdle bool) []*types.Impulse {
	if !isIdle {
		return nil // only explore ideas during idle time
	}

	idea := s.GetRandom()
	if idea == nil {
		return nil
	}

	return []*types.Impulse{
		{
			ID:          fmt.Sprintf("impulse-idea-%s", idea.ID),
			Source:      types.ImpulseIdea,
			Type:        "explore",
			Intensity:   0.3, // low priority - only during idle
			Timestamp:   time.Now(),
			Description: fmt.Sprintf("Explore: %s", idea.Idea),
			Data: map[string]any{
				"idea_id":    idea.ID,
				"idea":       idea.Idea,
				"sparked_by": idea.SparkBy,
				"priority":   idea.Priority,
			},
		},
	}
}

// Count returns the number of ideas
func (s *IdeaStore) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.ideas)
}

// CountUnexplored returns the number of unexplored ideas
func (s *IdeaStore) CountUnexplored() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	count := 0
	for _, i := range s.ideas {
		if !i.Explored {
			count++
		}
	}
	return count
}
