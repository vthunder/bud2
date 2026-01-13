package metacog

import (
	"fmt"
	"strings"
	"time"
)

// JournalEntry represents an entry for reflection analysis
type JournalEntry struct {
	ID        string    `json:"id"`
	Timestamp time.Time `json:"timestamp"`
	Type      string    `json:"type"` // decision, action, observation, exploration
	Summary   string    `json:"summary"`
	Context   string    `json:"context,omitempty"`
	Outcome   string    `json:"outcome,omitempty"`
	Reasoning string    `json:"reasoning,omitempty"`
}

// Reflection represents a metacognitive reflection
type Reflection struct {
	Timestamp   time.Time `json:"timestamp"`
	Period      string    `json:"period"` // "day", "week"
	Insights    []string  `json:"insights"`
	Suggestions []string  `json:"suggestions"`
	Patterns    []string  `json:"patterns"`
}

// Reflector performs metacognitive reflection on journal entries
type Reflector struct {
	// Generator interface for LLM-based reflection
	generator Generator
}

// Generator is the interface for LLM text generation
type Generator interface {
	Generate(prompt string) (string, error)
}

// NewReflector creates a new reflector
func NewReflector(generator Generator) *Reflector {
	return &Reflector{generator: generator}
}

// ReflectOnDay analyzes today's journal entries
func (r *Reflector) ReflectOnDay(entries []JournalEntry) (*Reflection, error) {
	if len(entries) == 0 {
		return &Reflection{
			Timestamp: time.Now(),
			Period:    "day",
			Insights:  []string{"No activity recorded today."},
		}, nil
	}

	// Simple reflection without LLM
	reflection := &Reflection{
		Timestamp: time.Now(),
		Period:    "day",
	}

	// Count by type
	typeCounts := make(map[string]int)
	for _, e := range entries {
		typeCounts[e.Type]++
	}

	// Generate insights
	if typeCounts["decision"] > 0 {
		reflection.Insights = append(reflection.Insights,
			fmt.Sprintf("Made %d decisions today", typeCounts["decision"]))
	}
	if typeCounts["action"] > 0 {
		reflection.Insights = append(reflection.Insights,
			fmt.Sprintf("Completed %d actions", typeCounts["action"]))
	}
	if typeCounts["exploration"] > 0 {
		reflection.Insights = append(reflection.Insights,
			fmt.Sprintf("Explored %d topics", typeCounts["exploration"]))
	}

	// If LLM available, do deeper reflection
	if r.generator != nil {
		deepReflection, err := r.deepReflect(entries, "day")
		if err == nil && deepReflection != nil {
			reflection.Insights = append(reflection.Insights, deepReflection.Insights...)
			reflection.Suggestions = deepReflection.Suggestions
			reflection.Patterns = deepReflection.Patterns
		}
	}

	return reflection, nil
}

// ReflectOnWeek analyzes the past week's journal entries
func (r *Reflector) ReflectOnWeek(entries []JournalEntry) (*Reflection, error) {
	if len(entries) == 0 {
		return &Reflection{
			Timestamp: time.Now(),
			Period:    "week",
			Insights:  []string{"No activity recorded this week."},
		}, nil
	}

	// Group by day
	dayGroups := make(map[string][]JournalEntry)
	for _, e := range entries {
		day := e.Timestamp.Format("2006-01-02")
		dayGroups[day] = append(dayGroups[day], e)
	}

	reflection := &Reflection{
		Timestamp: time.Now(),
		Period:    "week",
	}

	// Count totals
	var totalDecisions, totalActions int
	for _, dayEntries := range dayGroups {
		for _, e := range dayEntries {
			if e.Type == "decision" {
				totalDecisions++
			}
			if e.Type == "action" {
				totalActions++
			}
		}
	}

	reflection.Insights = []string{
		fmt.Sprintf("Active on %d days this week", len(dayGroups)),
		fmt.Sprintf("Made %d decisions total", totalDecisions),
		fmt.Sprintf("Completed %d actions total", totalActions),
	}

	// If LLM available, do deeper reflection
	if r.generator != nil {
		deepReflection, err := r.deepReflect(entries, "week")
		if err == nil && deepReflection != nil {
			reflection.Insights = append(reflection.Insights, deepReflection.Insights...)
			reflection.Suggestions = deepReflection.Suggestions
			reflection.Patterns = deepReflection.Patterns
		}
	}

	return reflection, nil
}

// deepReflect uses LLM to generate deeper insights
func (r *Reflector) deepReflect(entries []JournalEntry, period string) (*Reflection, error) {
	// Build summary of entries
	var summaries []string
	for _, e := range entries {
		summary := fmt.Sprintf("- [%s] %s: %s",
			e.Timestamp.Format("01/02 15:04"),
			e.Type,
			e.Summary)
		if e.Outcome != "" {
			summary += fmt.Sprintf(" (outcome: %s)", e.Outcome)
		}
		summaries = append(summaries, summary)
	}

	prompt := fmt.Sprintf(`Review my activity log for the past %s and provide insights:

%s

Provide:
1. 2-3 key observations about patterns in my behavior
2. 1-2 suggestions for improvement
3. Any recurring themes or topics

Be concise and actionable.`, period, strings.Join(summaries, "\n"))

	response, err := r.generator.Generate(prompt)
	if err != nil {
		return nil, err
	}

	// Parse response (simple approach)
	reflection := &Reflection{
		Timestamp: time.Now(),
		Period:    period,
	}

	lines := strings.Split(response, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Add non-empty lines as insights
		if strings.HasPrefix(line, "-") || strings.HasPrefix(line, "•") ||
			strings.HasPrefix(line, "1") || strings.HasPrefix(line, "2") ||
			strings.HasPrefix(line, "3") {
			reflection.Insights = append(reflection.Insights, strings.TrimLeft(line, "-•123. "))
		}
	}

	return reflection, nil
}

// SuggestReflexImprovements analyzes reflex performance and suggests improvements
func (r *Reflector) SuggestReflexImprovements(reflexStats []ReflexStat) []string {
	var suggestions []string

	for _, stat := range reflexStats {
		// Check for low success rate
		if stat.FireCount > 10 && stat.SuccessRate < 0.8 {
			suggestions = append(suggestions,
				fmt.Sprintf("Reflex '%s' has %.0f%% success rate - consider reviewing trigger pattern",
					stat.Name, stat.SuccessRate*100))
		}

		// Check for unused reflexes
		if stat.FireCount == 0 && stat.DaysSinceCreated > 7 {
			suggestions = append(suggestions,
				fmt.Sprintf("Reflex '%s' hasn't fired in %d days - consider removing or updating",
					stat.Name, stat.DaysSinceCreated))
		}

		// Check for very frequent reflexes
		if stat.FiresPerDay > 20 {
			suggestions = append(suggestions,
				fmt.Sprintf("Reflex '%s' fires %.1f times/day - consider if this should be a default behavior",
					stat.Name, stat.FiresPerDay))
		}
	}

	return suggestions
}

// ReflexStat holds statistics about a reflex for analysis
type ReflexStat struct {
	Name             string
	FireCount        int
	SuccessRate      float64
	DaysSinceCreated int
	FiresPerDay      float64
}
