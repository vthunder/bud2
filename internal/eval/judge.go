// Package eval provides independent evaluation of memory retrieval quality.
package eval

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/vthunder/bud2/internal/embedding"
	"github.com/vthunder/bud2/internal/graph"
)

// Judge evaluates memory relevance independently of self-eval.
type Judge struct {
	llm     *embedding.Client
	graphDB *graph.DB
}

// NewJudge creates a new memory judge.
func NewJudge(llm *embedding.Client, graphDB *graph.DB) *Judge {
	return &Judge{
		llm:     llm,
		graphDB: graphDB,
	}
}

// JudgeResult holds the evaluation result for a single memory.
type JudgeResult struct {
	TraceID    string `json:"trace_id"`
	Query      string `json:"query"`
	Memory     string `json:"memory"`
	SelfRating int    `json:"self_rating"`
	JudgeRating int   `json:"judge_rating"`
	Difference int    `json:"difference"` // judge - self
}

// SampleReport summarizes a batch evaluation.
type SampleReport struct {
	Timestamp      time.Time      `json:"timestamp"`
	SampleSize     int            `json:"sample_size"`
	SelfAvg        float64        `json:"self_avg"`
	JudgeAvg       float64        `json:"judge_avg"`
	Bias           float64        `json:"bias"`           // judge_avg - self_avg
	Correlation    float64        `json:"correlation"`    // Pearson correlation
	Agreement      float64        `json:"agreement"`      // % within 1 point
	Results        []JudgeResult  `json:"results"`
	Outliers       []JudgeResult  `json:"outliers"`       // |difference| >= 2
}

// judgePrompt is the prompt template for rating memory relevance.
const judgePrompt = `Rate how relevant this memory is to the given query.

Query: %s

Memory: %s

Rating scale:
1 = Completely irrelevant, would distract from the task
2 = Tangentially related but not useful for this query
3 = Somewhat relevant, provides minor helpful context
4 = Relevant and useful for addressing the query
5 = Highly relevant and valuable for this specific query

Respond with ONLY a single number (1-5).`

// JudgeMemory rates a single memory's relevance to a query.
func (j *Judge) JudgeMemory(query, memoryContent string) (int, error) {
	prompt := fmt.Sprintf(judgePrompt, query, memoryContent)

	response, err := j.llm.Generate(prompt)
	if err != nil {
		return 0, fmt.Errorf("LLM generate: %w", err)
	}

	// Parse rating from response
	response = strings.TrimSpace(response)

	// Try to extract just the number
	re := regexp.MustCompile(`^(\d)`)
	matches := re.FindStringSubmatch(response)
	if len(matches) < 2 {
		return 0, fmt.Errorf("could not parse rating from response: %q", response)
	}

	rating, err := strconv.Atoi(matches[1])
	if err != nil {
		return 0, fmt.Errorf("invalid rating number: %w", err)
	}

	if rating < 1 || rating > 5 {
		return 0, fmt.Errorf("rating out of range: %d", rating)
	}

	return rating, nil
}

// memoryEvalEntry represents a memory_eval entry from activity.jsonl
type memoryEvalEntry struct {
	Timestamp time.Time              `json:"ts"`
	Type      string                 `json:"type"`
	Data      memoryEvalData         `json:"data"`
}

type memoryEvalData struct {
	Eval     map[string]interface{} `json:"eval"`
	Resolved map[string]int         `json:"resolved"` // trace_id -> rating
}

// executiveWakeEntry represents an executive_wake entry
type executiveWakeEntry struct {
	Timestamp time.Time              `json:"ts"`
	Type      string                 `json:"type"`
	ThreadID  string                 `json:"thread_id"`
	Data      map[string]interface{} `json:"data"`
}

// EvaluateSample runs batch evaluation on recent memory retrievals.
func (j *Judge) EvaluateSample(activityPath string, sampleSize int) (*SampleReport, error) {
	// Load recent memory_eval entries
	evals, err := j.loadMemoryEvals(activityPath, sampleSize*2) // load extra to ensure we have enough
	if err != nil {
		return nil, fmt.Errorf("load memory evals: %w", err)
	}

	// Load executive_wake entries to get query context
	wakes, err := j.loadExecutiveWakes(activityPath)
	if err != nil {
		return nil, fmt.Errorf("load executive wakes: %w", err)
	}

	var results []JudgeResult
	var totalSelf, totalJudge float64

	// Process each eval entry
	for _, eval := range evals {
		if len(results) >= sampleSize {
			break
		}

		// Find the query context from the nearest executive_wake before this eval
		query := j.findQueryContext(eval.Timestamp, wakes)
		if query == "" {
			continue // skip if we can't find context
		}

		// Process each trace rating in this eval
		for traceID, selfRating := range eval.Data.Resolved {
			if len(results) >= sampleSize {
				break
			}

			// Get trace content
			trace, err := j.graphDB.GetTrace(traceID)
			if err != nil || trace == nil {
				continue // skip if trace not found
			}

			// Judge this memory
			judgeRating, err := j.JudgeMemory(query, trace.Summary)
			if err != nil {
				continue // skip on error
			}

			result := JudgeResult{
				TraceID:     traceID,
				Query:       truncate(query, 100),
				Memory:      truncate(trace.Summary, 100),
				SelfRating:  selfRating,
				JudgeRating: judgeRating,
				Difference:  judgeRating - selfRating,
			}
			results = append(results, result)
			totalSelf += float64(selfRating)
			totalJudge += float64(judgeRating)
		}
	}

	if len(results) == 0 {
		return nil, fmt.Errorf("no valid samples found")
	}

	// Calculate statistics
	n := float64(len(results))
	selfAvg := totalSelf / n
	judgeAvg := totalJudge / n

	// Calculate agreement (within 1 point)
	var agreement int
	var outliers []JudgeResult
	for _, r := range results {
		if abs(r.Difference) <= 1 {
			agreement++
		}
		if abs(r.Difference) >= 2 {
			outliers = append(outliers, r)
		}
	}

	// Calculate Pearson correlation
	correlation := j.pearsonCorrelation(results)

	return &SampleReport{
		Timestamp:   time.Now(),
		SampleSize:  len(results),
		SelfAvg:     selfAvg,
		JudgeAvg:    judgeAvg,
		Bias:        judgeAvg - selfAvg,
		Correlation: correlation,
		Agreement:   float64(agreement) / n,
		Results:     results,
		Outliers:    outliers,
	}, nil
}

func (j *Judge) loadMemoryEvals(path string, limit int) ([]memoryEvalEntry, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var entries []memoryEvalEntry
	scanner := bufio.NewScanner(file)
	// Increase buffer size for long lines
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	for scanner.Scan() {
		var entry memoryEvalEntry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			continue
		}
		if entry.Type == "memory_eval" && entry.Data.Resolved != nil {
			entries = append(entries, entry)
		}
	}

	// Return most recent entries
	if len(entries) > limit {
		entries = entries[len(entries)-limit:]
	}

	return entries, scanner.Err()
}

func (j *Judge) loadExecutiveWakes(path string) ([]executiveWakeEntry, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var entries []executiveWakeEntry
	scanner := bufio.NewScanner(file)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	for scanner.Scan() {
		var entry executiveWakeEntry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			continue
		}
		if entry.Type == "executive_wake" {
			entries = append(entries, entry)
		}
	}

	return entries, scanner.Err()
}

func (j *Judge) findQueryContext(evalTime time.Time, wakes []executiveWakeEntry) string {
	// Find the most recent wake before this eval
	var bestWake *executiveWakeEntry
	for i := range wakes {
		if wakes[i].Timestamp.Before(evalTime) {
			if bestWake == nil || wakes[i].Timestamp.After(bestWake.Timestamp) {
				bestWake = &wakes[i]
			}
		}
	}

	if bestWake == nil {
		return ""
	}

	// Extract context from wake data
	if ctx, ok := bestWake.Data["context"].(string); ok {
		return ctx
	}

	return ""
}

func (j *Judge) pearsonCorrelation(results []JudgeResult) float64 {
	if len(results) < 2 {
		return 0
	}

	n := float64(len(results))
	var sumX, sumY, sumXY, sumX2, sumY2 float64

	for _, r := range results {
		x := float64(r.SelfRating)
		y := float64(r.JudgeRating)
		sumX += x
		sumY += y
		sumXY += x * y
		sumX2 += x * x
		sumY2 += y * y
	}

	numerator := n*sumXY - sumX*sumY
	denominator := (n*sumX2 - sumX*sumX) * (n*sumY2 - sumY*sumY)

	if denominator <= 0 {
		return 0
	}

	return numerator / sqrt(denominator)
}

func sqrt(x float64) float64 {
	if x <= 0 {
		return 0
	}
	// Newton-Raphson
	z := x / 2
	for i := 0; i < 10; i++ {
		z = z - (z*z-x)/(2*z)
	}
	return z
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
