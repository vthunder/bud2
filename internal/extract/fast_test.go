package extract

import (
	"testing"

	"github.com/vthunder/bud2/internal/graph"
)

func TestPersonSkipFiltering(t *testing.T) {
	e := NewFastExtractor()

	tests := []struct {
		text     string
		wantName string
		wantSkip []string // names that should NOT appear
	}{
		{
			text:     "Remind me to call Sarah about the Denver project next Monday",
			wantName: "Sarah",
			wantSkip: []string{"me", "call"},
		},
		{
			text:     "I talked to Microsoft about an Azure deployment",
			wantSkip: []string{"Microsoft"}, // Should not appear as person
		},
		{
			text:     "Hey @john, can you review this PR?",
			wantName: "john",
			wantSkip: []string{},
		},
		{
			text:     "I'm going to say a few things to test the new memory store",
			wantSkip: []string{"say", "test", "things"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.text[:30], func(t *testing.T) {
			entities := e.Extract(tc.text)

			// Check wanted name is present
			if tc.wantName != "" {
				found := false
				for _, ent := range entities {
					if ent.Name == tc.wantName && ent.Type == graph.EntityPerson {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("Expected to find person %q", tc.wantName)
				}
			}

			// Check skipped names are not present as persons
			for _, skip := range tc.wantSkip {
				for _, ent := range entities {
					if ent.Name == skip && ent.Type == graph.EntityPerson {
						t.Errorf("Should not have extracted %q as person, got %+v", skip, ent)
					}
				}
			}
		})
	}
}

func TestExtraction(t *testing.T) {
	e := NewFastExtractor()

	t.Run("extraction results", func(t *testing.T) {
		texts := []string{
			"Remind me to call Sarah about the Denver project next Monday",
			"I talked to Microsoft about an Azure deployment",
			"Hey @john, can you review this PR?",
			"I'm going to say a few things to test the new memory store",
		}

		for _, text := range texts {
			t.Logf("\n--- %q ---", text)
			entities := e.Extract(text)
			for _, ent := range entities {
				t.Logf("  %s [%s] conf=%.2f", ent.Name, ent.Type, ent.Confidence)
			}
		}
	})
}
