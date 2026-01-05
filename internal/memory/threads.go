package memory

import (
	"encoding/json"
	"os"
	"sync"

	"github.com/vthunder/bud2/internal/types"
)

// ThreadPool manages threads
type ThreadPool struct {
	mu      sync.RWMutex
	threads map[string]*types.Thread
	path    string
}

// NewThreadPool creates a new thread pool
func NewThreadPool(path string) *ThreadPool {
	return &ThreadPool{
		threads: make(map[string]*types.Thread),
		path:    path,
	}
}

// Add adds a thread to the pool
func (t *ThreadPool) Add(thread *types.Thread) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.threads[thread.ID] = thread
}

// Get retrieves a thread by ID
func (t *ThreadPool) Get(id string) *types.Thread {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.threads[id]
}

// Delete removes a thread
func (t *ThreadPool) Delete(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.threads, id)
}

// ByStatus returns threads with a given status
func (t *ThreadPool) ByStatus(status types.ThreadStatus) []*types.Thread {
	t.mu.RLock()
	defer t.mu.RUnlock()

	result := make([]*types.Thread, 0)
	for _, thread := range t.threads {
		if thread.Status == status {
			result = append(result, thread)
		}
	}
	return result
}

// Active returns the active thread (should be at most one)
func (t *ThreadPool) Active() *types.Thread {
	threads := t.ByStatus(types.StatusActive)
	if len(threads) > 0 {
		return threads[0]
	}
	return nil
}

// All returns all threads
func (t *ThreadPool) All() []*types.Thread {
	t.mu.RLock()
	defer t.mu.RUnlock()

	result := make([]*types.Thread, 0, len(t.threads))
	for _, thread := range t.threads {
		result = append(result, thread)
	}
	return result
}

// AddPerceptRef adds a percept reference to a thread
func (t *ThreadPool) AddPerceptRef(threadID, perceptID string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	thread, ok := t.threads[threadID]
	if !ok {
		return false
	}

	// Check if already referenced
	for _, ref := range thread.PerceptRefs {
		if ref == perceptID {
			return true // already there
		}
	}

	thread.PerceptRefs = append(thread.PerceptRefs, perceptID)
	return true
}

// Load reads threads from disk
func (t *ThreadPool) Load() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	data, err := os.ReadFile(t.path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}

	var file struct {
		Threads []*types.Thread `json:"threads"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return err
	}

	t.threads = make(map[string]*types.Thread)
	for _, thread := range file.Threads {
		t.threads[thread.ID] = thread
	}
	return nil
}

// Save writes threads to disk
func (t *ThreadPool) Save() error {
	t.mu.RLock()
	defer t.mu.RUnlock()

	file := struct {
		Threads []*types.Thread `json:"threads"`
	}{
		Threads: make([]*types.Thread, 0, len(t.threads)),
	}
	for _, thread := range t.threads {
		file.Threads = append(file.Threads, thread)
	}

	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(t.path, data, 0644)
}
