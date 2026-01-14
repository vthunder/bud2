package extract

import (
	"testing"
)

func TestProseExtractor(t *testing.T) {
	e := NewProseExtractor()

	tests := []struct {
		text     string
		wantName string
		// Note: prose's type classification isn't always accurate, so we just check extraction
	}{
		{
			text:     "Lebron James plays basketball in Los Angeles.",
			wantName: "Lebron James",
		},
		{
			text:     "I talked to Microsoft about an Azure deployment",
			wantName: "Microsoft",
		},
		{
			text:     "George Washington was born in Virginia.",
			wantName: "George Washington",
		},
	}

	for _, tc := range tests {
		t.Run(tc.text[:30], func(t *testing.T) {
			entities := e.Extract(tc.text)
			t.Logf("Extracted from %q:", tc.text)
			for _, ent := range entities {
				t.Logf("  %s [%s] conf=%.2f", ent.Name, ent.Type, ent.Confidence)
			}

			// Check if expected entity is present (any type)
			found := false
			for _, ent := range entities {
				if ent.Name == tc.wantName {
					found = true
					break
				}
			}
			if tc.wantName != "" && !found {
				t.Errorf("Expected to find %q", tc.wantName)
			}
		})
	}
}

func TestProseExtractorComprehensive(t *testing.T) {
	e := NewProseExtractor()

	texts := []string{
		"Remind me to call Sarah about the Denver project next Monday",
		"I talked to Microsoft about an Azure deployment",
		"George Washington was born on February 22, 1732 in Virginia.",
		"Apple announced a new iPhone at their Cupertino headquarters.",
		"The New York Times reported that $500 million was invested.",
	}

	for _, text := range texts {
		t.Run(text[:30], func(t *testing.T) {
			entities := e.Extract(text)
			t.Logf("\n--- %q ---", text)
			if len(entities) == 0 {
				t.Log("  (no entities extracted)")
			}
			for _, ent := range entities {
				t.Logf("  %s [%s] conf=%.2f pos=%d-%d", ent.Name, ent.Type, ent.Confidence, ent.StartPos, ent.EndPos)
			}
		})
	}
}
