package buffer

import (
	"strings"
)

// OllamaSummarizer wraps an embedding client for buffer summarization
type OllamaSummarizer struct {
	client interface {
		Summarize(fragments []string) (string, error)
	}
}

// NewOllamaSummarizer creates a summarizer using the embedding client
func NewOllamaSummarizer(client interface {
	Summarize(fragments []string) (string, error)
}) *OllamaSummarizer {
	return &OllamaSummarizer{client: client}
}

// Summarize implements the Summarizer interface for conversation buffers
func (s *OllamaSummarizer) Summarize(content string) (string, error) {
	// Split content into fragments (lines)
	lines := strings.Split(content, "\n")
	var fragments []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			fragments = append(fragments, line)
		}
	}

	if len(fragments) == 0 {
		return "", nil
	}

	return s.client.Summarize(fragments)
}

// NullSummarizer is a no-op summarizer for testing or when summarization is disabled
type NullSummarizer struct{}

// Summarize returns the content unchanged
func (s *NullSummarizer) Summarize(content string) (string, error) {
	// Just truncate if too long
	if len(content) > 500 {
		return content[:500] + "...", nil
	}
	return content, nil
}
