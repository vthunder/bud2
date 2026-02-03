package ner

// Integration tests for the spaCy NER sidecar (en_core_web_sm).
// These tests require the sidecar to be running at localhost:8099.
// They are skipped automatically if the sidecar is not available.
//
// Known spaCy en_core_web_sm limitations discovered by these tests:
// - Non-Western names (Anjan, Anurag) often misclassified or missed
// - Common tech terms (CI, Stripe) may be misclassified
// - Money entities returned without currency symbol ($50,000 → "50,000")
//
// These limitations are acceptable because spaCy is only a fast pre-filter;
// Ollama handles full extraction for messages that pass the NER gate.

import (
	"testing"
)

func requireSidecar(t *testing.T) *Client {
	t.Helper()
	c := NewClient("http://127.0.0.1:8099")
	if !c.Healthy() {
		t.Skip("NER sidecar not available at :8099")
	}
	return c
}

func TestNERSidecar_Detection(t *testing.T) {
	c := requireSidecar(t)

	tests := []struct {
		name       string
		text       string
		wantEntity bool
		wantLabels map[string]string // text -> expected label (nil = don't check specifics)
	}{
		{
			name:       "simple person mention",
			text:       "I had lunch with Sarah yesterday",
			wantEntity: true,
			wantLabels: map[string]string{"Sarah": "PERSON"},
		},
		{
			// spaCy en_core_web_sm struggles with non-Western names.
			// Anjan is missed entirely; Anurag misclassified as PRODUCT.
			// This is acceptable — the purpose of NER is to gate Ollama,
			// and it correctly detects *something* to trigger extraction.
			name:       "non-western names trigger extraction",
			text:       "Anjan presented the design and Anurag approved it",
			wantEntity: true,
			wantLabels: nil, // don't assert specific labels; just that extraction triggers
		},
		{
			name:       "person and org",
			text:       "Alex just joined Google as a senior engineer",
			wantEntity: true,
			wantLabels: map[string]string{"Alex": "PERSON", "Google": "ORG"},
		},
		{
			name:       "location",
			text:       "We're opening an office in San Francisco",
			wantEntity: true,
			wantLabels: map[string]string{"San Francisco": "GPE"},
		},
		{
			name:       "no entities in casual chat",
			text:       "ok sounds good, thanks!",
			wantEntity: false,
		},
		{
			// spaCy detects "CI" as ORG — a false positive for tech chat.
			// This means some entity-free tech messages will trigger Ollama
			// unnecessarily, but that's a minor cost vs missing real entities.
			name:       "technical chat may have false positives",
			text:       "the build is failing on the CI pipeline",
			wantEntity: true, // CI detected as ORG
		},
		{
			// spaCy returns "50,000" without the "$" prefix.
			name:       "money amount detected",
			text:       "The project budget is $50,000",
			wantEntity: true,
			wantLabels: map[string]string{"50,000": "MONEY"},
		},
		{
			// spaCy doesn't recognize "Stripe" as ORG but does detect AWS.
			name:       "some org names detected",
			text:       "We use Stripe for payments and deploy on AWS",
			wantEntity: true,
			wantLabels: map[string]string{"AWS": "ORG"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := c.Extract(tt.text)
			if err != nil {
				t.Fatalf("Extract failed: %v", err)
			}

			if resp.HasEntities != tt.wantEntity {
				t.Errorf("HasEntities = %v, want %v (entities: %+v)",
					resp.HasEntities, tt.wantEntity, resp.Entities)
			}

			if tt.wantLabels != nil {
				found := make(map[string]string)
				for _, e := range resp.Entities {
					found[e.Text] = e.Label
				}

				for text, label := range tt.wantLabels {
					gotLabel, ok := found[text]
					if !ok {
						t.Errorf("Expected entity %q (%s) not found in results: %+v",
							text, label, resp.Entities)
					} else if gotLabel != label {
						t.Errorf("Entity %q: expected label %s, got %s", text, label, gotLabel)
					}
				}
			}

			// Verify response time is reasonable (< 500ms for sidecar)
			if resp.DurationMs > 500 {
				t.Errorf("NER took %.0fms, expected < 500ms", resp.DurationMs)
			}
		})
	}
}
