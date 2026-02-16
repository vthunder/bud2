package extract

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/vthunder/bud2/memory-service/pkg/graph"
)

// InvalidationResult represents the result of checking for contradictions
type InvalidationResult struct {
	InvalidatedIDs []int64 `json:"invalidated_ids"`
	Reason         string  `json:"reason,omitempty"`
}

// Invalidator detects contradictions between new and existing facts
type Invalidator struct {
	generator Generator
}

// NewInvalidator creates a new invalidation detector
func NewInvalidator(generator Generator) *Invalidator {
	return &Invalidator{generator: generator}
}

const invalidationPrompt = `You are analyzing knowledge graph relationships to detect contradictions.

A NEW FACT has been stated. Compare it against EXISTING FACTS to determine if any existing facts are now invalid (contradicted by the new information).

IMPORTANT GUIDELINES:
- Only mark a fact as invalidated if there is CLEAR CONTRADICTION
- Do NOT invalidate facts just because they are not mentioned - absence is not contradiction
- Facts about the same subject can coexist unless they directly conflict
- Time-based updates (deadlines moved, emails changed) DO invalidate the old value
- Preferences or attributes that change over time DO invalidate old values

EXAMPLES OF CONTRADICTIONS:
- "X lives in Portland" contradicts "X lives in Seattle" (can only live in one place)
- "X's email is new@example.com" contradicts "X's email is old@example.com"
- "Deadline is March 30" contradicts "Deadline is March 15" (same deadline, different date)
- "X works at CompanyB" contradicts "X works at CompanyA" (if exclusive employment)

EXAMPLES OF NON-CONTRADICTIONS:
- "X knows Python" does NOT contradict "X knows Go" (can know multiple languages)
- "X is friends with A" does NOT contradict "X is friends with B" (can have multiple friends)
- "X visited Paris" does NOT contradict "X visited London" (can visit multiple places)

NEW FACT:
%s → [%s] → %s

EXISTING FACTS:
%s

Return ONLY valid JSON with this structure:
{"invalidated_ids": [1, 2], "reason": "Brief explanation of why these facts are contradicted"}

If no facts are contradicted, return:
{"invalidated_ids": [], "reason": "No contradiction found"}

JSON:`

// CheckInvalidation checks if a new relation contradicts any existing relations
func (inv *Invalidator) CheckInvalidation(
	newSubject, newPredicate, newObject string,
	candidates []graph.EntityRelation,
	entityNames map[string]string, // entityID -> name
) (*InvalidationResult, error) {
	if inv.generator == nil || len(candidates) == 0 {
		return &InvalidationResult{InvalidatedIDs: nil}, nil
	}

	// Build existing facts description
	var existingFacts strings.Builder
	for _, c := range candidates {
		fromName := entityNames[c.FromID]
		if fromName == "" {
			fromName = c.FromID
		}
		toName := entityNames[c.ToID]
		if toName == "" {
			toName = c.ToID
		}
		fmt.Fprintf(&existingFacts, "[ID=%d] %s → [%s] → %s\n",
			c.ID, fromName, c.RelationType, toName)
	}

	prompt := fmt.Sprintf(invalidationPrompt,
		newSubject, newPredicate, newObject,
		existingFacts.String())

	response, err := inv.generator.Generate(prompt)
	if err != nil {
		return nil, fmt.Errorf("invalidation check failed: %w", err)
	}

	// Parse response
	response = strings.TrimSpace(response)
	if strings.HasPrefix(response, "```json") {
		response = strings.TrimPrefix(response, "```json")
		response = strings.TrimSuffix(response, "```")
		response = strings.TrimSpace(response)
	} else if strings.HasPrefix(response, "```") {
		response = strings.TrimPrefix(response, "```")
		response = strings.TrimSuffix(response, "```")
		response = strings.TrimSpace(response)
	}

	// Find JSON object
	start := strings.Index(response, "{")
	end := strings.LastIndex(response, "}")
	if start < 0 || end < start {
		return &InvalidationResult{InvalidatedIDs: nil}, nil
	}

	var result InvalidationResult
	if err := json.Unmarshal([]byte(response[start:end+1]), &result); err != nil {
		return &InvalidationResult{InvalidatedIDs: nil}, nil
	}

	return &result, nil
}

// IsExclusiveRelation returns true if the relation type typically allows only one object.
// With meta-relationships, located_in and kin_of can be exclusive in some contexts.
// The invalidation LLM prompt handles the nuance of whether a specific instance contradicts.
func IsExclusiveRelation(relType graph.EdgeType) bool {
	switch relType {
	case graph.EdgeLocatedIn,
		graph.EdgeKinOf,
		// Legacy types (in case old data exists)
		graph.EdgeLivesIn,
		graph.EdgeMarriedTo,
		graph.EdgeWorksAt:
		return true
	default:
		return false
	}
}
