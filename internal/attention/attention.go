package attention

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/vthunder/bud2/internal/embedding"
	"github.com/vthunder/bud2/internal/memory"
	"github.com/vthunder/bud2/internal/types"
)

// Attention selects which thread should be active
type Attention struct {
	percepts  *memory.PerceptPool
	threads   *memory.ThreadPool
	traces    *memory.TracePool
	embedder  *embedding.Client
	arousal   *types.Arousal
	onChange  func(*types.Thread) // called when active thread changes
	stopChan  chan struct{}
	tickRate  time.Duration
}

// Config holds attention configuration
type Config struct {
	TickRate        time.Duration // how often to re-evaluate
	OllamaURL       string        // Ollama base URL (default: http://localhost:11434)
	EmbeddingModel  string        // embedding model (default: nomic-embed-text)
}

// New creates a new Attention system
func New(percepts *memory.PerceptPool, threads *memory.ThreadPool, traces *memory.TracePool, onChange func(*types.Thread)) *Attention {
	return NewWithConfig(percepts, threads, traces, onChange, Config{})
}

// NewWithConfig creates a new Attention system with custom config
func NewWithConfig(percepts *memory.PerceptPool, threads *memory.ThreadPool, traces *memory.TracePool, onChange func(*types.Thread), cfg Config) *Attention {
	return &Attention{
		percepts: percepts,
		threads:  threads,
		traces:   traces,
		embedder: embedding.NewClient(cfg.OllamaURL, cfg.EmbeddingModel),
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
			// Decay activation for all threads
			a.decayActivation()

			// Decay activation for all traces
			a.traces.DecayActivation(0.995) // slower decay than threads

			// Recompute salience for all threads
			a.updateAllSalience()

			// Select highest salience thread
			selected := a.selectThread()

			// Debug: log thread selection periodically (every ~10 seconds)
			if time.Now().Second()%10 == 0 {
				threads := a.threads.All()
				var withNew, processed []string
				for _, t := range threads {
					if t.Status == types.StatusActive || t.Status == types.StatusPaused {
						id := t.ID
						if len(id) > 20 {
							id = id[:20]
						}
						entry := fmt.Sprintf("%s(%.2f)", id, t.Salience)
						if t.ProcessedAt == nil {
							withNew = append(withNew, entry)
						} else {
							processed = append(processed, entry)
						}
					}
				}
				if len(withNew) > 0 || len(processed) > 0 {
					selectedID := "nil"
					hasNew := false
					if selected != nil {
						selectedID = selected.ID
						if len(selectedID) > 20 {
							selectedID = selectedID[:20]
						}
						hasNew = selected.ProcessedAt == nil
					}
					log.Printf("[attention-debug] new=%v processed=%v selected=%s(new=%v) threshold=%.2f",
						withNew, processed, selectedID, hasNew, 0.6-(a.arousal.Level*0.3))
				}
			}

			// Notify if:
			// 1. Active thread changed, OR
			// 2. Current thread has new unprocessed content
			if selected != nil {
				threadChanged := selected.ID != lastActive
				hasNewContent := selected.ProcessedAt == nil

				if threadChanged || hasNewContent {
					if threadChanged {
						a.activateThread(selected)
						lastActive = selected.ID
					}
					if a.onChange != nil {
						a.onChange(selected)
					}
				}
			}
		}
	}
}

// decayActivation reduces activation levels over time
func (a *Attention) decayActivation() {
	decayRate := 0.99 // per tick (100ms), so ~0.9 per second
	for _, thread := range a.threads.All() {
		if thread.Status == types.StatusComplete {
			continue
		}
		thread.Activation *= decayRate
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

	// Check for high-priority tags and urgency
	tagBoost := 0.0
	for _, p := range percepts {
		// Tag-based boosts
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

		// Urgency boost based on intensity (for reminders, high-priority tasks)
		// P1 tasks (intensity >= 0.9) beat owner messages
		// P2 urgent (intensity >= 0.8) equals owner messages
		if p.Intensity >= 0.9 {
			tagBoost = max(tagBoost, 0.3)
		} else if p.Intensity >= 0.8 {
			tagBoost = max(tagBoost, 0.2)
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
	// Also separate threads with new content (ProcessedAt == nil) from already-processed ones
	var withNewContent []*types.Thread
	var alreadyProcessed []*types.Thread
	for _, t := range threads {
		if t.Status == types.StatusActive || t.Status == types.StatusPaused {
			if t.ProcessedAt == nil {
				withNewContent = append(withNewContent, t)
			} else {
				alreadyProcessed = append(alreadyProcessed, t)
			}
		}
	}

	// Prioritize threads with new content - these need attention
	// Only consider already-processed threads if no new content threads exist
	candidates := withNewContent
	if len(candidates) == 0 {
		candidates = alreadyProcessed
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
	top := candidates[0]
	if top.Salience >= threshold {
		return top
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

// RoutePercept routes a percept to the best matching thread (or creates new one)
// This is the main entry point for handling new percepts.
// Note: Percepts go to ONE thread only. Other threads get context via trace activation
// (spreading activation) and mid-thread trace loading (BUD-wk3).
func (a *Attention) RoutePercept(percept *types.Percept, goalGenerator func(string) string) []*types.Thread {
	const associationThreshold = 0.3 // minimum score to join existing thread

	// Compute embedding for this percept
	content := ""
	if c, ok := percept.Data["content"].(string); ok {
		content = c
	}
	if content != "" {
		emb, err := a.embedder.Embed(content)
		if err != nil {
			log.Printf("[attention] Failed to embed percept %s: %v", percept.ID, err)
		} else {
			percept.Embedding = emb
		}
	}

	// Spreading activation: boost traces similar to this percept
	// This makes related context available to ALL threads via GetActivatedTraces
	if len(percept.Embedding) > 0 {
		activated := a.traces.SpreadActivation(percept.Embedding, 0.5, 0.3)
		if len(activated) > 0 {
			log.Printf("[attention] Activated %d traces from percept %s", len(activated), percept.ID)
		}
	}

	// Find best matching thread above threshold
	var bestThread *types.Thread
	var bestScore float64

	for _, thread := range a.threads.All() {
		if thread.Status == types.StatusComplete || thread.Status == types.StatusFrozen {
			continue
		}

		score := a.computeAssociation(percept, thread)
		if score >= associationThreshold && score > bestScore {
			bestThread = thread
			bestScore = score
		}
	}

	// Route to best matching thread
	if bestThread != nil {
		a.addPerceptToThread(bestThread, percept)
		// Tag percept with thread ID for consolidation clustering
		a.tagPerceptWithThread(percept, bestThread.ID)
		log.Printf("[attention] Routed percept %s to thread %s (association: %.2f)",
			percept.ID, bestThread.ID, bestScore)
		return []*types.Thread{bestThread}
	}

	// No matching threads, create new one
	goal := goalGenerator(content)
	thread := a.createThread(goal, percept)
	// Tag percept with thread ID for consolidation clustering
	a.tagPerceptWithThread(percept, thread.ID)
	log.Printf("[attention] Created new thread %s for percept %s (no threads above threshold)",
		thread.ID, percept.ID)
	return []*types.Thread{thread}
}

// tagPerceptWithThread sets the conversation_id feature for consolidation clustering
func (a *Attention) tagPerceptWithThread(percept *types.Percept, threadID string) {
	if percept.Features == nil {
		percept.Features = make(map[string]any)
	}
	percept.Features["conversation_id"] = threadID
}

// computeAssociation calculates how strongly a percept associates with a thread
func (a *Attention) computeAssociation(percept *types.Percept, thread *types.Thread) float64 {
	score := 0.0
	var maxSimilarity float64 // track max embedding similarity for decay override

	// Source match (discord, github, etc)
	if weight, ok := thread.Features.Sources[percept.Source]; ok {
		score += weight * 0.15
	}

	// Channel match (strong signal for Discord)
	if channelID, ok := percept.Data["channel_id"].(string); ok {
		if weight, ok := thread.Features.Channels[channelID]; ok {
			score += weight * 0.3
		}
	}

	// Author match
	if authorID, ok := percept.Data["author_id"].(string); ok {
		if weight, ok := thread.Features.Authors[authorID]; ok {
			score += weight * 0.2
		}
	}

	// Semantic similarity via embeddings (most important signal)
	if len(percept.Embedding) > 0 {
		// Compare with thread centroid (accumulated percept embeddings)
		if len(thread.Embeddings.Centroid) > 0 {
			similarity := embedding.CosineSimilarity(percept.Embedding, thread.Embeddings.Centroid)
			if similarity > maxSimilarity {
				maxSimilarity = similarity
			}
			// Similarity is -1 to 1, normalize to 0-0.3 contribution
			score += (similarity + 1) / 2 * 0.3
		}
		// Compare with thread topic (goal embedding)
		if len(thread.Embeddings.Topic) > 0 {
			similarity := embedding.CosineSimilarity(percept.Embedding, thread.Embeddings.Topic)
			if similarity > maxSimilarity {
				maxSimilarity = similarity
			}
			score += (similarity + 1) / 2 * 0.2
		}
	}

	// Current activation level contributes
	score += thread.Activation * 0.15

	// Check for back-reference language (signals intent to continue old topic)
	content := ""
	if c, ok := percept.Data["content"].(string); ok {
		content = c
	}
	hasBackReference := detectBackReference(content)

	// Apply time decay as multiplier (old threads need much higher base score to match)
	recencySeconds := time.Since(thread.LastActive).Seconds()
	var decay float64
	switch {
	case recencySeconds < 60:
		decay = 1.0 // no decay for threads active in last minute
	case recencySeconds < 300:
		decay = 0.8 // 20% decay for 1-5 minutes old
	case recencySeconds < 1800:
		decay = 0.4 // 60% decay for 5-30 minutes old
	default:
		decay = 0.15 // 85% decay for threads >30 minutes old
	}

	// Override decay floor if there's a back-reference or very high semantic similarity
	// This allows reviving old threads when user explicitly references them
	if hasBackReference || maxSimilarity > 0.85 {
		if decay < 0.5 {
			decay = 0.5
		}
	}

	score *= decay

	return score
}

// backReferencePatterns are phrases that signal intent to continue a previous topic
var backReferencePatterns = []string{
	"about that",
	"about the",
	"regarding that",
	"regarding the",
	"going back to",
	"back to the",
	"as we discussed",
	"as we talked about",
	"as i mentioned",
	"as you mentioned",
	"like i said",
	"like you said",
	"remember when",
	"remember that",
	"earlier you",
	"earlier we",
	"before you",
	"before we",
	"you were saying",
	"i was saying",
	"we were talking",
	"we were discussing",
	"continuing from",
	"following up on",
	"to follow up",
	"circling back",
	"on that note",
	"speaking of which",
	"on that topic",
	"about earlier",
}

// detectBackReference checks if content contains language referencing a previous conversation
func detectBackReference(content string) bool {
	lower := strings.ToLower(content)
	for _, pattern := range backReferencePatterns {
		if strings.Contains(lower, pattern) {
			return true
		}
	}
	return false
}

// addPerceptToThread adds a percept to an existing thread and updates features
func (a *Attention) addPerceptToThread(thread *types.Thread, percept *types.Percept) {
	// Add percept reference
	thread.PerceptRefs = append(thread.PerceptRefs, percept.ID)

	// Boost activation
	thread.Activation += 0.5 + percept.Intensity*0.5

	// Update features (with decay for old features)
	a.updateThreadFeatures(thread, percept)

	// Update centroid embedding with exponential moving average
	if len(percept.Embedding) > 0 {
		thread.Embeddings.Centroid = embedding.UpdateCentroid(
			thread.Embeddings.Centroid,
			percept.Embedding,
			0.3, // alpha - weight of new embedding
		)
	}

	// Update timestamp
	thread.LastActive = time.Now()

	// Clear ProcessedAt since there's new content
	thread.ProcessedAt = nil
}

// createThread creates a new thread initialized with a percept's features
func (a *Attention) createThread(goal string, percept *types.Percept) *types.Thread {
	thread := &types.Thread{
		ID:          generateThreadID(),
		Goal:        goal,
		Status:      types.StatusPaused,
		Activation:  1.0, // new threads start with full activation
		PerceptRefs: []string{percept.ID},
		State: types.ThreadState{
			Phase:   "new",
			Context: make(map[string]any),
		},
		Features: types.ThreadFeatures{
			Channels: make(map[string]float64),
			Authors:  make(map[string]float64),
			Sources:  make(map[string]float64),
		},
		Embeddings: types.ThreadEmbeddings{},
		CreatedAt:  time.Now(),
		LastActive: time.Now(),
	}

	// Initialize features from percept
	a.updateThreadFeatures(thread, percept)

	// Initialize centroid from percept embedding
	if len(percept.Embedding) > 0 {
		thread.Embeddings.Centroid = percept.Embedding
	}

	// Generate topic embedding from goal
	if goal != "" {
		topicEmb, err := a.embedder.Embed(goal)
		if err != nil {
			log.Printf("[attention] Failed to embed goal for thread %s: %v", thread.ID, err)
		} else {
			thread.Embeddings.Topic = topicEmb
		}
	}

	a.threads.Add(thread)
	return thread
}

// updateThreadFeatures accumulates features from a percept into thread
func (a *Attention) updateThreadFeatures(thread *types.Thread, percept *types.Percept) {
	// Ensure maps are initialized
	if thread.Features.Channels == nil {
		thread.Features.Channels = make(map[string]float64)
	}
	if thread.Features.Authors == nil {
		thread.Features.Authors = make(map[string]float64)
	}
	if thread.Features.Sources == nil {
		thread.Features.Sources = make(map[string]float64)
	}

	// Decay existing features slightly (newer percepts have more influence)
	decayFactor := 0.9
	for k := range thread.Features.Channels {
		thread.Features.Channels[k] *= decayFactor
	}
	for k := range thread.Features.Authors {
		thread.Features.Authors[k] *= decayFactor
	}
	for k := range thread.Features.Sources {
		thread.Features.Sources[k] *= decayFactor
	}

	// Add new features
	thread.Features.Sources[percept.Source] += 0.5

	if channelID, ok := percept.Data["channel_id"].(string); ok {
		thread.Features.Channels[channelID] += 0.5
	}

	if authorID, ok := percept.Data["author_id"].(string); ok {
		thread.Features.Authors[authorID] += 0.5
	}

	// Cap feature weights at 1.0
	for k := range thread.Features.Channels {
		if thread.Features.Channels[k] > 1.0 {
			thread.Features.Channels[k] = 1.0
		}
	}
	for k := range thread.Features.Authors {
		if thread.Features.Authors[k] > 1.0 {
			thread.Features.Authors[k] = 1.0
		}
	}
	for k := range thread.Features.Sources {
		if thread.Features.Sources[k] > 1.0 {
			thread.Features.Sources[k] = 1.0
		}
	}
}

// CreateThread creates a new thread for a percept (legacy method, prefer RoutePercept)
func (a *Attention) CreateThread(goal string, perceptRefs []string) *types.Thread {
	thread := &types.Thread{
		ID:          generateThreadID(),
		Goal:        goal,
		Status:      types.StatusPaused,
		Activation:  1.0,
		PerceptRefs: perceptRefs,
		State: types.ThreadState{
			Phase:   "new",
			Context: make(map[string]any),
		},
		Features: types.ThreadFeatures{
			Channels: make(map[string]float64),
			Authors:  make(map[string]float64),
			Sources:  make(map[string]float64),
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

func generateTraceID() string {
	return fmt.Sprintf("tr-%d", time.Now().UnixNano())
}

func max(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

// summarizeContent compresses content for trace storage
// For now, simple truncation - LLM summarization can be added later
func summarizeContent(content string) string {
	const maxLen = 300 // enough to capture key info
	if len(content) <= maxLen {
		return content
	}
	// Truncate at word boundary
	truncated := content[:maxLen]
	lastSpace := -1
	for i := len(truncated) - 1; i >= 0; i-- {
		if truncated[i] == ' ' {
			lastSpace = i
			break
		}
	}
	if lastSpace > maxLen/2 {
		truncated = truncated[:lastSpace]
	}
	return truncated + "..."
}

// truncate shortens a string for logging
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// Consolidate runs memory consolidation - clusters and summarizes percepts into traces
// Should be called during idle periods (no active threads)
func (a *Attention) Consolidate() int {
	const minPerceptAge = 30 * time.Second

	// Get all percepts older than threshold that haven't been consolidated
	allPercepts := a.percepts.All()
	var candidates []*types.Percept
	cutoff := time.Now().Add(-minPerceptAge)
	for _, p := range allPercepts {
		if p.Timestamp.Before(cutoff) && len(p.Embedding) > 0 {
			if !a.traces.HasSource(p.ID) {
				candidates = append(candidates, p)
			}
		}
	}

	return a.consolidatePercepts(candidates)
}

// ConsolidateAll runs memory consolidation ignoring age requirement
// Used during shutdown to ensure all percepts are consolidated
func (a *Attention) ConsolidateAll() int {
	// Get all percepts with embeddings that haven't been consolidated
	allPercepts := a.percepts.All()
	var candidates []*types.Percept
	for _, p := range allPercepts {
		if len(p.Embedding) > 0 && !a.traces.HasSource(p.ID) {
			candidates = append(candidates, p)
		}
	}

	return a.consolidatePercepts(candidates)
}

// consolidatePercepts clusters and summarizes percepts into traces
// Implements biological memory mechanisms:
// - Reconsolidation: if labile trace + correction language, update in place
// - Inhibition: if similar but no explicit correction, new trace inhibits old
func (a *Attention) consolidatePercepts(candidates []*types.Percept) int {
	const (
		reinforceThreshold   = 0.8 // similarity to reinforce existing trace
		labileMatchThreshold = 0.6 // similarity to match labile trace for correction
	)

	if len(candidates) == 0 {
		return 0
	}

	consolidated := 0
	var forClustering []*types.Percept

	// First pass: check for reconsolidation opportunities (correction of labile trace)
	for _, percept := range candidates {
		content := ""
		if c, ok := percept.Data["content"].(string); ok {
			content = c
		}

		// Check for correction language
		if hasCorrection(content) && len(percept.Embedding) > 0 {
			// Find labile traces similar to this percept
			labileMatches := a.findLabileTraces(percept.Embedding, labileMatchThreshold)
			if len(labileMatches) > 0 {
				// RECONSOLIDATION: Update the labile trace in place
				// Pick the most similar labile trace
				bestTrace := labileMatches[0]
				bestSim := embedding.CosineSimilarity(percept.Embedding, bestTrace.Embedding)
				for _, t := range labileMatches[1:] {
					sim := embedding.CosineSimilarity(percept.Embedding, t.Embedding)
					if sim > bestSim {
						bestTrace = t
						bestSim = sim
					}
				}

				// Update the trace with corrected content
				bestTrace.Sources = append(bestTrace.Sources, percept.ID)
				bestTrace.Embedding = embedding.UpdateCentroid(bestTrace.Embedding, percept.Embedding, 0.5) // higher weight for correction
				bestTrace.LastAccess = time.Now()

				// Re-summarize with the correction
				a.resummarizeTrace(bestTrace)

				log.Printf("[consolidate] RECONSOLIDATION: Updated trace %s with correction", bestTrace.ID)
				consolidated++
				continue // handled, don't cluster
			}
		}

		// No reconsolidation - check for regular reinforcement
		similar := a.traces.FindSimilar(percept.Embedding, reinforceThreshold)
		if len(similar) > 0 {
			// Find the best matching trace
			bestTrace := similar[0]
			bestSim := embedding.CosineSimilarity(percept.Embedding, bestTrace.Embedding)
			for _, t := range similar[1:] {
				sim := embedding.CosineSimilarity(percept.Embedding, t.Embedding)
				if sim > bestSim {
					bestTrace = t
					bestSim = sim
				}
			}

			// Check if this is an implicit correction (similar content, labile trace, but no correction language)
			if bestTrace.IsLabile() && !bestTrace.IsCore {
				// INHIBITION: Create new trace that inhibits the old one
				// The old info might be outdated
				forClustering = append(forClustering, percept)
				// Mark that the new cluster should inhibit this trace
				if percept.Features == nil {
					percept.Features = make(map[string]any)
				}
				percept.Features["inhibits_trace"] = bestTrace.ID
				log.Printf("[consolidate] Will create inhibiting trace for labile trace %s", bestTrace.ID)
				continue
			}

			// Regular reinforcement (non-labile trace)
			a.traces.Reinforce(bestTrace.ID, 0.3)
			bestTrace.Embedding = embedding.UpdateCentroid(bestTrace.Embedding, percept.Embedding, 0.2)
			bestTrace.Sources = append(bestTrace.Sources, percept.ID)

			// Re-summarize the trace with new content
			a.resummarizeTrace(bestTrace)

			log.Printf("[consolidate] Reinforced trace %s (strength: %d)", bestTrace.ID, bestTrace.Strength)
			consolidated++
		} else {
			// No matching trace, queue for clustering
			forClustering = append(forClustering, percept)
		}
	}

	if len(forClustering) == 0 {
		return consolidated
	}

	// Second pass: cluster remaining percepts by conversation_id
	clusters := a.clusterPercepts(forClustering)

	// Create one trace per cluster
	for _, cluster := range clusters {
		trace := a.createTraceFromCluster(cluster)
		if trace != nil {
			a.traces.Add(trace)
			log.Printf("[consolidate] Created trace %s: %s", trace.ID, truncate(trace.Content, 80))
			consolidated++
		}
	}

	return consolidated
}

// clusterPercepts groups percepts by conversation_id (thread ID)
// Percepts in the same thread cluster together for summarization
// Falls back to per-percept clusters if no conversation_id is set
func (a *Attention) clusterPercepts(percepts []*types.Percept) [][]*types.Percept {
	if len(percepts) == 0 {
		return nil
	}

	// Group by feature key (senses define what features to use for clustering)
	// Currently we use "conversation_id" as the primary clustering feature
	clusterMap := make(map[string][]*types.Percept)
	var noFeaturePercepts []*types.Percept

	for _, p := range percepts {
		// Get conversation_id feature (or any other clustering feature)
		clusterKey := ""
		if p.Features != nil {
			if convID, ok := p.Features["conversation_id"].(string); ok {
				clusterKey = convID
			}
		}

		if clusterKey == "" {
			// No clustering feature - each percept is its own cluster
			noFeaturePercepts = append(noFeaturePercepts, p)
		} else {
			clusterMap[clusterKey] = append(clusterMap[clusterKey], p)
		}
	}

	// Build result
	var clusters [][]*types.Percept
	for _, cluster := range clusterMap {
		clusters = append(clusters, cluster)
	}

	// Add percepts without features as individual clusters
	for _, p := range noFeaturePercepts {
		clusters = append(clusters, []*types.Percept{p})
	}

	return clusters
}

// createTraceFromCluster creates a trace from a cluster of percepts
func (a *Attention) createTraceFromCluster(cluster []*types.Percept) *types.Trace {
	if len(cluster) == 0 {
		return nil
	}

	// Collect content fragments, source IDs, and inhibition targets
	var fragments []string
	var sources []string
	var embeddings [][]float64
	var inhibits []string
	inhibitSet := make(map[string]bool)

	for _, p := range cluster {
		sources = append(sources, p.ID)
		embeddings = append(embeddings, p.Embedding)
		if content, ok := p.Data["content"].(string); ok && content != "" {
			// Include speaker context for better summarization
			speaker := "user"
			if p.Source == "bud" {
				speaker = "Bud"
			} else if author, ok := p.Data["author"].(string); ok && author != "" {
				speaker = author
			}
			fragments = append(fragments, fmt.Sprintf("%s: %s", speaker, content))
		}

		// Check for inhibition marker
		if p.Features != nil {
			if inhibitID, ok := p.Features["inhibits_trace"].(string); ok {
				if !inhibitSet[inhibitID] {
					inhibits = append(inhibits, inhibitID)
					inhibitSet[inhibitID] = true
				}
			}
		}
	}

	// Summarize using LLM
	var summary string
	if len(fragments) > 0 {
		var err error
		summary, err = a.embedder.Summarize(fragments)
		if err != nil {
			log.Printf("[consolidate] Summarization failed, using truncation: %v", err)
			// Fallback to concatenation + truncation
			summary = summarizeContent(joinFragments(fragments))
		} else {
			summary = strings.TrimSpace(summary)
		}
	}

	// Compute cluster centroid embedding
	centroid := embedding.AverageEmbeddings(embeddings)

	trace := &types.Trace{
		ID:         generateTraceID(),
		Content:    summary,
		Embedding:  centroid,
		Activation: 0.5,
		Strength:   len(cluster), // strength reflects cluster size
		Sources:    sources,
		CreatedAt:  time.Now(),
		LastAccess: time.Now(),
	}

	// Set inhibition links if any
	if len(inhibits) > 0 {
		trace.Inhibits = inhibits
		log.Printf("[consolidate] INHIBITION: New trace %s inhibits traces: %v", trace.ID, inhibits)
	}

	return trace
}

// resummarizeTrace updates a trace's content based on all its sources
func (a *Attention) resummarizeTrace(trace *types.Trace) {
	// Collect all source content with speaker context
	var fragments []string
	for _, srcID := range trace.Sources {
		if p := a.percepts.Get(srcID); p != nil {
			if content, ok := p.Data["content"].(string); ok && content != "" {
				// Include speaker context for better summarization
				speaker := "user"
				if p.Source == "bud" {
					speaker = "Bud"
				} else if author, ok := p.Data["author"].(string); ok && author != "" {
					speaker = author
				}
				fragments = append(fragments, fmt.Sprintf("%s: %s", speaker, content))
			}
		}
	}

	if len(fragments) == 0 {
		return
	}

	// Re-summarize
	summary, err := a.embedder.Summarize(fragments)
	if err != nil {
		log.Printf("[consolidate] Re-summarization failed: %v", err)
		return
	}

	trace.Content = strings.TrimSpace(summary)
}

// joinFragments concatenates fragments with newlines
func joinFragments(fragments []string) string {
	result := ""
	for i, f := range fragments {
		if i > 0 {
			result += "\n"
		}
		result += f
	}
	return result
}

// correctionPatterns are phrases that indicate a correction/update to previous information
var correctionPatterns = []string{
	"actually",
	"actually,",
	"correction",
	"correction:",
	"i was wrong",
	"i made a mistake",
	"that's wrong",
	"that was wrong",
	"not really",
	"no wait",
	"no, wait",
	"scratch that",
	"forget what i said",
	"let me correct",
	"to correct",
	"the correct",
	"should be",
	"is actually",
	"it's actually",
	"meant to say",
	"i meant",
}

// hasCorrection checks if content contains correction language
func hasCorrection(content string) bool {
	lower := strings.ToLower(content)
	for _, pattern := range correctionPatterns {
		if strings.Contains(lower, pattern) {
			return true
		}
	}
	return false
}

// findLabileTraces finds traces that are currently labile and similar to the given embedding
func (a *Attention) findLabileTraces(emb []float64, threshold float64) []*types.Trace {
	allTraces := a.traces.All()
	var labile []*types.Trace

	for _, t := range allTraces {
		if !t.IsLabile() || t.IsCore {
			continue
		}
		if len(t.Embedding) > 0 && len(emb) > 0 {
			sim := embedding.CosineSimilarity(emb, t.Embedding)
			if sim >= threshold {
				labile = append(labile, t)
			}
		}
	}

	return labile
}

// GetActivatedTraces returns traces that are currently activated
// excludeSources filters out traces that contain any of the given source IDs
// contextEmb is the thread's current centroid - used to re-activate relevant traces
// Marks returned traces as labile (open for reconsolidation) for 5 minutes
func (a *Attention) GetActivatedTraces(limit int, excludeSources []string, contextEmb []float64) []*types.Trace {
	// Re-run spreading activation based on current thread context
	// This ensures traces relevant to the evolved conversation are activated
	if len(contextEmb) > 0 {
		a.traces.SpreadActivation(contextEmb, 0.3, 0.3) // moderate boost for context-similar traces
	}

	traces := a.traces.GetActivated(0.1, 0) // low threshold, get all activated

	// Build exclude set
	excludeSet := make(map[string]bool)
	for _, src := range excludeSources {
		excludeSet[src] = true
	}

	// Filter out traces sourced from excluded percepts AND inhibited traces
	var filtered []*types.Trace
	activatedIDs := make(map[string]bool)
	for _, t := range traces {
		activatedIDs[t.ID] = true
	}

	for _, t := range traces {
		// Skip if sourced from excluded percepts
		excluded := false
		for _, src := range t.Sources {
			if excludeSet[src] {
				excluded = true
				break
			}
		}
		if excluded {
			continue
		}

		// Skip if inhibited by another activated trace
		inhibited := false
		for _, other := range traces {
			if other.ID == t.ID {
				continue
			}
			for _, inhibitedID := range other.Inhibits {
				if inhibitedID == t.ID && activatedIDs[other.ID] {
					// This trace is inhibited by another activated trace
					// Only suppress if the inhibitor is stronger
					if other.Strength >= t.Strength {
						inhibited = true
						break
					}
				}
			}
			if inhibited {
				break
			}
		}
		if inhibited {
			continue
		}

		filtered = append(filtered, t)
		if limit > 0 && len(filtered) >= limit {
			break
		}
	}

	// Mark filtered traces as labile (reconsolidation window)
	const labileWindow = 5 * time.Minute
	for _, t := range filtered {
		if !t.IsCore { // core traces are immutable
			t.MakeLabile(labileWindow)
		}
	}

	return filtered
}

// TraceCount returns the number of traces
func (a *Attention) TraceCount() int {
	return a.traces.Count()
}

// EmbedPercept computes the embedding for a percept without routing it
func (a *Attention) EmbedPercept(percept *types.Percept) {
	content := ""
	if c, ok := percept.Data["content"].(string); ok {
		content = c
	}
	if content != "" {
		emb, err := a.embedder.Embed(content)
		if err != nil {
			log.Printf("[attention] Failed to embed percept %s: %v", percept.ID, err)
		} else {
			percept.Embedding = emb
		}
	}
}

// BootstrapCore loads core identity traces from a seed file if none exist
// Seed file format: markdown with entries separated by "---" lines
// Each entry becomes a core trace
func (a *Attention) BootstrapCore(seedPath string) error {
	// Check if core traces already exist
	if a.traces.HasCore() {
		log.Printf("[attention] Core traces already exist, skipping bootstrap")
		return nil
	}

	// Read seed file
	file, err := os.Open(seedPath)
	if os.IsNotExist(err) {
		log.Printf("[attention] No seed file at %s, skipping bootstrap", seedPath)
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to open seed file: %w", err)
	}
	defer file.Close()

	// Parse entries separated by "---"
	// Lines starting with # are section headers (for human readability) and are stripped
	var entries []string
	var current strings.Builder
	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "---" {
			if current.Len() > 0 {
				entries = append(entries, strings.TrimSpace(current.String()))
				current.Reset()
			}
		} else if strings.HasPrefix(trimmed, "#") {
			// Skip markdown headers - they're for human readability only
			continue
		} else {
			current.WriteString(line)
			current.WriteString("\n")
		}
	}
	// Don't forget last entry
	if current.Len() > 0 {
		entries = append(entries, strings.TrimSpace(current.String()))
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("failed to read seed file: %w", err)
	}

	// Create core traces from entries
	for i, content := range entries {
		if content == "" {
			continue
		}

		// Compute embedding for the content
		emb, err := a.embedder.Embed(content)
		if err != nil {
			log.Printf("[attention] Warning: failed to embed core trace %d: %v", i, err)
		}

		trace := &types.Trace{
			ID:         fmt.Sprintf("core-%d-%d", time.Now().UnixNano(), i),
			Content:    content,
			Embedding:  emb,
			Activation: 1.0, // core traces start fully activated
			Strength:   100, // high strength so they don't get pruned
			IsCore:     true,
			CreatedAt:  time.Now(),
			LastAccess: time.Now(),
		}
		a.traces.Add(trace)
		log.Printf("[attention] Created core trace: %s", truncate(content, 60))
	}

	log.Printf("[attention] Bootstrapped %d core traces from %s", len(entries), seedPath)
	return nil
}

// GetCoreTraces returns all core identity traces
func (a *Attention) GetCoreTraces() []*types.Trace {
	return a.traces.GetCore()
}

// SetTraceCore marks a trace as core or removes core status
func (a *Attention) SetTraceCore(traceID string, isCore bool) bool {
	return a.traces.SetCore(traceID, isCore)
}

// CreateImmediateTrace creates a trace that's immediately available
// (bypasses consolidation delay) for reflex context continuity
func (a *Attention) CreateImmediateTrace(content string, source string) *types.Trace {
	// Generate embedding
	emb, err := a.embedder.Embed(content)
	if err != nil {
		log.Printf("[attention] Failed to embed immediate trace: %v", err)
	}

	trace := &types.Trace{
		ID:         generateTraceID(),
		Content:    content,
		Sources:    []string{source},
		Embedding:  emb,
		Activation: 0.8, // High activation for immediate relevance
		Strength:   1,
		CreatedAt:  time.Now(),
		LastAccess: time.Now(),
	}

	a.traces.Add(trace)
	log.Printf("[attention] Created immediate trace: %s", truncate(content, 50))

	return trace
}
