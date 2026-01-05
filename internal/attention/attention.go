package attention

import (
	"log"
	"sort"
	"time"

	"github.com/vthunder/bud2/internal/memory"
	"github.com/vthunder/bud2/internal/types"
)

// Attention selects which thread should be active
type Attention struct {
	percepts  *memory.PerceptPool
	threads   *memory.ThreadPool
	arousal   *types.Arousal
	onChange  func(*types.Thread) // called when active thread changes
	stopChan  chan struct{}
	tickRate  time.Duration
}

// Config holds attention configuration
type Config struct {
	TickRate time.Duration // how often to re-evaluate
}

// New creates a new Attention system
func New(percepts *memory.PerceptPool, threads *memory.ThreadPool, onChange func(*types.Thread)) *Attention {
	return &Attention{
		percepts: percepts,
		threads:  threads,
		arousal: &types.Arousal{
			Level: 0.5,
			Factors: types.ArousalFactors{
				UserWaiting:    false,
				RecentErrors:   0,
				BudgetPressure: false,
			},
		},
		onChange: onChange,
		stopChan: make(chan struct{}),
		tickRate: 100 * time.Millisecond,
	}
}

// Start begins the attention loop
func (a *Attention) Start() {
	go a.loop()
	log.Println("[attention] Started")
}

// Stop halts attention
func (a *Attention) Stop() {
	close(a.stopChan)
}

// SetArousal updates arousal level
func (a *Attention) SetArousal(arousal *types.Arousal) {
	a.arousal = arousal
}

func (a *Attention) loop() {
	ticker := time.NewTicker(a.tickRate)
	defer ticker.Stop()

	var lastActive string

	for {
		select {
		case <-a.stopChan:
			return
		case <-ticker.C:
			// Recompute salience for all threads
			a.updateAllSalience()

			// Select highest salience thread
			selected := a.selectThread()

			// If active thread changed, notify
			if selected != nil && selected.ID != lastActive {
				a.activateThread(selected)
				lastActive = selected.ID
				if a.onChange != nil {
					a.onChange(selected)
				}
			}
		}
	}
}

// updateAllSalience recomputes salience for all threads
func (a *Attention) updateAllSalience() {
	for _, thread := range a.threads.All() {
		if thread.Status == types.StatusComplete {
			continue
		}
		thread.Salience = a.computeSalience(thread)
	}
}

// computeSalience calculates thread salience from its referenced percepts
func (a *Attention) computeSalience(thread *types.Thread) float64 {
	// Base salience from thread age (older paused threads decay)
	baseSalience := 0.5
	if thread.Status == types.StatusPaused {
		age := time.Since(thread.LastActive).Minutes()
		baseSalience = max(0.1, 0.5-age*0.05) // decay 0.05 per minute
	}
	if thread.Status == types.StatusFrozen {
		baseSalience = 0.1
	}

	// Boost from referenced percepts
	perceptBoost := 0.0
	percepts := a.percepts.GetMany(thread.PerceptRefs)
	for _, p := range percepts {
		// Intensity contributes directly
		contribution := p.Intensity * 0.3

		// Recency boosts (recent percepts matter more)
		recencySeconds := p.Recency()
		if recencySeconds < 60 {
			contribution *= 1.5 // very recent
		} else if recencySeconds < 300 {
			contribution *= 1.0 // somewhat recent
		} else {
			contribution *= 0.5 // older
		}

		perceptBoost += contribution
	}

	// Normalize percept boost (diminishing returns)
	if perceptBoost > 0.5 {
		perceptBoost = 0.5 + (perceptBoost-0.5)*0.5
	}

	// Check for high-priority tags
	tagBoost := 0.0
	for _, p := range percepts {
		for _, tag := range p.Tags {
			switch tag {
			case "from:owner":
				tagBoost = max(tagBoost, 0.2)
			case "mention":
				tagBoost = max(tagBoost, 0.15)
			case "dm":
				tagBoost = max(tagBoost, 0.1)
			}
		}
	}

	salience := baseSalience + perceptBoost + tagBoost

	// Cap at 1.0
	if salience > 1.0 {
		salience = 1.0
	}

	return salience
}

// selectThread picks the highest-salience thread that should be active
func (a *Attention) selectThread() *types.Thread {
	threads := a.threads.All()
	if len(threads) == 0 {
		return nil
	}

	// Filter to active/paused threads (frozen threads can't become active directly)
	candidates := make([]*types.Thread, 0)
	for _, t := range threads {
		if t.Status == types.StatusActive || t.Status == types.StatusPaused {
			candidates = append(candidates, t)
		}
	}

	if len(candidates) == 0 {
		return nil
	}

	// Sort by salience descending
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Salience > candidates[j].Salience
	})

	// Attention threshold based on arousal
	// High arousal = low threshold (more things break through)
	// Low arousal = high threshold (stay focused)
	threshold := 0.6 - (a.arousal.Level * 0.3) // 0.3-0.6 range

	current := a.threads.Active()

	// If we have an active thread, require significant salience difference to switch
	if current != nil {
		top := candidates[0]
		if top.ID == current.ID {
			return current // keep current
		}

		// Need to beat current by threshold margin
		if top.Salience > current.Salience+threshold*0.5 {
			return top
		}
		return current
	}

	// No active thread, pick highest salience if above threshold
	if candidates[0].Salience >= threshold {
		return candidates[0]
	}

	return nil
}

// activateThread makes a thread active (pausing current if any)
func (a *Attention) activateThread(thread *types.Thread) {
	// Pause current active thread
	current := a.threads.Active()
	if current != nil && current.ID != thread.ID {
		current.Status = types.StatusPaused
		log.Printf("[attention] Paused thread %s (salience: %.2f)", current.ID, current.Salience)
	}

	// Activate new thread
	thread.Status = types.StatusActive
	thread.LastActive = time.Now()
	log.Printf("[attention] Activated thread %s (salience: %.2f)", thread.ID, thread.Salience)
}

// CreateThread creates a new thread for a percept
func (a *Attention) CreateThread(goal string, perceptRefs []string) *types.Thread {
	thread := &types.Thread{
		ID:          generateThreadID(),
		Goal:        goal,
		Status:      types.StatusPaused, // starts paused, attention will activate if appropriate
		PerceptRefs: perceptRefs,
		State: types.ThreadState{
			Phase:   "new",
			Context: make(map[string]any),
		},
		CreatedAt:  time.Now(),
		LastActive: time.Now(),
	}

	a.threads.Add(thread)
	log.Printf("[attention] Created thread %s: %s", thread.ID, goal)

	return thread
}

func generateThreadID() string {
	return "t-" + time.Now().Format("20060102-150405.000")
}

func max(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
