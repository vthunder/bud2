package metacog

import (
	"fmt"
	"regexp"
	"strings"
)

// RefLexProposal represents a proposed reflex generated from a pattern
type ReflexProposal struct {
	PatternID   string `json:"pattern_id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	YAML        string `json:"yaml"`
	Confidence  float64 `json:"confidence"`
}

// Compiler generates reflex proposals from detected patterns
type Compiler struct {
	detector *PatternDetector
}

// NewCompiler creates a new reflex compiler
func NewCompiler(detector *PatternDetector) *Compiler {
	return &Compiler{detector: detector}
}

// GenerateProposals creates reflex proposals from candidate patterns
func (c *Compiler) GenerateProposals() []*ReflexProposal {
	candidates := c.detector.GetCandidates()
	var proposals []*ReflexProposal

	for _, pattern := range candidates {
		proposal := c.generateProposal(pattern)
		if proposal != nil {
			proposals = append(proposals, proposal)
		}
	}

	return proposals
}

// generateProposal creates a reflex proposal from a pattern
func (c *Compiler) generateProposal(pattern *Pattern) *ReflexProposal {
	// Generate a regex pattern from the input example
	regexPattern := c.generateRegex(pattern.InputExample)
	if regexPattern == "" {
		return nil
	}

	// Generate a name from the category and pattern
	name := c.generateName(pattern)

	// Generate YAML
	yaml := c.generateYAML(name, pattern, regexPattern)

	return &ReflexProposal{
		PatternID:   pattern.ID,
		Name:        name,
		Description: fmt.Sprintf("Auto-compiled from %d occurrences (%.0f%% success)", pattern.Occurrences, pattern.SuccessRate()*100),
		YAML:        yaml,
		Confidence:  pattern.SuccessRate(),
	}
}

// generateRegex attempts to generalize an input example into a regex
func (c *Compiler) generateRegex(input string) string {
	// Simple approach: escape special chars, replace variable parts

	// Common patterns to generalize
	input = strings.ToLower(input)

	// Check for greeting patterns
	greetings := []string{"hi", "hey", "hello", "yo", "sup"}
	for _, g := range greetings {
		if strings.HasPrefix(input, g+" ") || input == g {
			return "(?i)^(hi|hey|hello|yo|sup)\\b"
		}
	}

	// Check for question patterns
	if strings.HasSuffix(input, "?") {
		// Keep the question structure but escape special chars
		escaped := regexp.QuoteMeta(strings.TrimSuffix(input, "?"))
		return "(?i)" + escaped + "\\??"
	}

	// Default: escape and make case-insensitive
	escaped := regexp.QuoteMeta(input)
	return "(?i)" + escaped
}

// generateName creates a reflex name from a pattern
func (c *Compiler) generateName(pattern *Pattern) string {
	// Use category if available
	if pattern.Category != "" {
		return fmt.Sprintf("auto_%s_%s", pattern.Category, pattern.ID[:8])
	}

	// Extract key words from input
	words := strings.Fields(strings.ToLower(pattern.InputExample))
	if len(words) > 0 {
		firstWord := strings.Trim(words[0], "?!.")
		return fmt.Sprintf("auto_%s_%s", firstWord, pattern.ID[:8])
	}

	return fmt.Sprintf("auto_%s", pattern.ID[:8])
}

// generateYAML creates the YAML definition for a reflex
func (c *Compiler) generateYAML(name string, pattern *Pattern, regexPattern string) string {
	// Escape the response for YAML
	response := strings.ReplaceAll(pattern.Response, "\"", "\\\"")
	response = strings.ReplaceAll(response, "\n", "\\n")

	yaml := fmt.Sprintf(`name: %s
description: "Auto-compiled pattern (occurrences: %d, success: %.0f%%)"
trigger:
  source: discord
  pattern: "%s"
pipeline:
  - action: reply
    params:
      message: "%s"
priority: 50
`, name, pattern.Occurrences, pattern.SuccessRate()*100, regexPattern, response)

	return yaml
}

// ProposalSummary returns a human-readable summary of a proposal
func (p *ReflexProposal) ProposalSummary() string {
	return fmt.Sprintf(`**Reflex Proposal: %s**
%s
Confidence: %.0f%%

Would you like me to create this reflex? (reply yes/no)`, p.Name, p.Description, p.Confidence*100)
}

// AcceptProposal marks a proposal as accepted and returns the pattern ID
func (c *Compiler) AcceptProposal(patternID string) {
	c.detector.MarkProposed(patternID)
}

// RejectProposal marks a proposal as rejected
func (c *Compiler) RejectProposal(patternID string) {
	c.detector.MarkRejected(patternID)
}
