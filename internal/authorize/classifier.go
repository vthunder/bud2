package authorize

import (
	"fmt"
	"log"
	"strings"

	"github.com/vthunder/bud2/internal/embedding"
)

// Classifier detects authorization patterns in text using local Ollama/qwen
type Classifier struct {
	ollama *embedding.Client
}

// NewClassifier creates a new authorization classifier
func NewClassifier(ollama *embedding.Client) *Classifier {
	return &Classifier{ollama: ollama}
}

// ClassifyText checks if text contains user authorization/approval to proceed
// Returns true if authorization is detected
func (c *Classifier) ClassifyText(text string) (bool, error) {
	if text == "" {
		return false, nil
	}

	prompt := `You are classifying text for authorization patterns.

Does this text contain user authorization or approval to proceed with something?
Look for phrases like: "go ahead", "do it", "yes", "proceed", "you can do it now", "approved", "confirmed", etc.

Answer only YES or NO.

Text:
` + text + `

Answer:`

	response, err := c.ollama.Generate(prompt)
	if err != nil {
		return false, fmt.Errorf("ollama generate: %w", err)
	}

	response = strings.TrimSpace(strings.ToUpper(response))
	return strings.HasPrefix(response, "YES"), nil
}

// AnnotateIfAuthorized checks text for authorization and wraps it with annotation if found
// Returns the (possibly annotated) text and whether authorization was detected
func (c *Classifier) AnnotateIfAuthorized(text string) (string, bool) {
	hasAuth, err := c.ClassifyText(text)
	if err != nil {
		log.Printf("[authorize] Classification error: %v", err)
		return text, false
	}

	if hasAuth {
		return fmt.Sprintf("[HISTORICAL - re-confirm before acting]\n%s", text), true
	}
	return text, false
}

// ProcessBufferEntries processes a list of formatted buffer entries (e.g. "Author: content")
// and annotates any that contain authorization
// Returns the annotated entries joined with newlines
func (c *Classifier) ProcessBufferEntries(entries []string) string {
	var result []string
	for _, entry := range entries {
		annotated, hasAuth := c.AnnotateIfAuthorized(entry)
		if hasAuth {
			log.Printf("[authorize] Detected authorization in: %s", truncate(entry, 50))
		}
		result = append(result, annotated)
	}
	return strings.Join(result, "\n")
}

// ProcessSummary annotates a buffer summary if it contains authorization
func (c *Classifier) ProcessSummary(summary string) string {
	if summary == "" {
		return summary
	}
	annotated, hasAuth := c.AnnotateIfAuthorized(summary)
	if hasAuth {
		log.Printf("[authorize] Detected authorization in summary")
	}
	return annotated
}

func truncate(s string, maxLen int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
