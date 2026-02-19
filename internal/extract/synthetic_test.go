package extract

// Synthetic test harness for entity/relationship extraction.
// Uses realistic Discord chat messages to validate extraction quality
// through the full ExtractAll pipeline (prompt → LLM parse → post-processing).

import (
	"testing"

	"github.com/vthunder/bud2/internal/graph"
)

// syntheticCase represents a realistic chat message with expected extraction results.
// The mockEntityResp/mockRelResp simulate what a well-tuned LLM should return.
// The expected* fields define what ExtractAll should produce after post-processing.
type syntheticCase struct {
	name            string
	message         string
	mockEntityResp  string // Mock LLM response for pass 1 (entities)
	mockRelResp     string // Mock LLM response for pass 2 (relationships)
	expectedEntities map[string]graph.EntityType // name -> type
	expectedRels     []expectedRel
}

type expectedRel struct {
	subject   string
	predicate string
	object    string
}

var syntheticCases = []syntheticCase{
	{
		name:    "project discussion with people and orgs",
		message: "Anjan presented the Nightshade rollup design today. Anurag was on board with the general direction.",
		mockEntityResp: `[
			{"name":"Anjan","type":"PERSON","confidence":0.95},
			{"name":"Nightshade","type":"PRODUCT","confidence":0.9},
			{"name":"Anurag","type":"PERSON","confidence":0.95}
		]`,
		mockRelResp: `[
			{"subject":"Anjan","predicate":"affiliated_with","object":"Nightshade","confidence":0.8}
		]`,
		expectedEntities: map[string]graph.EntityType{
			"Anjan":      graph.EntityPerson,
			"Nightshade": graph.EntityProduct,
			"Anurag":     graph.EntityPerson,
		},
		expectedRels: []expectedRel{
			{"Anjan", "affiliated_with", "Nightshade"},
		},
	},
	{
		name:    "casual mention of person and location",
		message: "I'm meeting Sarah in Portland tomorrow for coffee",
		mockEntityResp: `[
			{"name":"Sarah","type":"PERSON","confidence":0.95},
			{"name":"Portland","type":"GPE","confidence":0.95}
		]`,
		mockRelResp: `[
			{"subject":"speaker","predicate":"knows","object":"Sarah","confidence":0.9}
		]`,
		expectedEntities: map[string]graph.EntityType{
			"Sarah":    graph.EntityPerson,
			"Portland": graph.EntityGPE,
		},
		expectedRels: []expectedRel{
			{"speaker", "knows", "Sarah"},
		},
	},
	{
		name:    "technical discussion with org names",
		message: "We're using Stripe for payments and deploying on AWS. The Vercel integration is solid.",
		mockEntityResp: `[
			{"name":"Stripe","type":"ORG","confidence":0.99},
			{"name":"AWS","type":"ORG","confidence":0.99},
			{"name":"Vercel","type":"ORG","confidence":0.95}
		]`,
		mockRelResp: `[]`,
		expectedEntities: map[string]graph.EntityType{
			"Stripe": graph.EntityOrg,
			"AWS":    graph.EntityOrg,
			"Vercel": graph.EntityOrg,
		},
		expectedRels: nil,
	},
	{
		name:    "email and money in conversation",
		message: "Send the invoice to john@example.com, the total is $4,500",
		mockEntityResp: `[
			{"name":"john@example.com","type":"EMAIL","confidence":0.99},
			{"name":"$4,500","type":"MONEY","confidence":0.99}
		]`,
		mockRelResp: `[]`,
		expectedEntities: map[string]graph.EntityType{
			"john@example.com": graph.EntityEmail,
			"$4,500":           graph.EntityMoney,
		},
		expectedRels: nil,
	},
	{
		name:    "email missed by LLM but caught by regex",
		message: "Can you forward that to alice@company.org?",
		mockEntityResp: `[]`,
		mockRelResp:    "", // no pass 2 since no entities from LLM
		expectedEntities: map[string]graph.EntityType{
			"alice@company.org": graph.EntityEmail,
		},
		expectedRels: nil,
	},
	{
		name:    "noise entities filtered out",
		message: "I think the system is working well now",
		mockEntityResp: `[
			{"name":"I","type":"PERSON","confidence":0.5},
			{"name":"the system","type":"PRODUCT","confidence":0.6},
			{"name":"it","type":"OTHER","confidence":0.3}
		]`,
		mockRelResp: "",
		expectedEntities: map[string]graph.EntityType{},
		expectedRels: nil,
	},
	{
		name:    "generic product names filtered",
		message: "The new analytics dashboard looks great",
		mockEntityResp: `[
			{"name":"the new analytics dashboard","type":"PRODUCT","confidence":0.7}
		]`,
		mockRelResp: "",
		expectedEntities: map[string]graph.EntityType{},
		expectedRels: nil,
	},
	{
		name:    "person with org affiliation",
		message: "Alex just joined Google as a senior engineer. He was at Meta before.",
		mockEntityResp: `[
			{"name":"Alex","type":"PERSON","confidence":0.95},
			{"name":"Google","type":"ORG","confidence":0.99},
			{"name":"Meta","type":"ORG","confidence":0.99}
		]`,
		mockRelResp: `[
			{"subject":"Alex","predicate":"affiliated_with","object":"Google","confidence":0.95},
			{"subject":"Alex","predicate":"affiliated_with","object":"Meta","confidence":0.8}
		]`,
		expectedEntities: map[string]graph.EntityType{
			"Alex":   graph.EntityPerson,
			"Google": graph.EntityOrg,
			"Meta":   graph.EntityOrg,
		},
		expectedRels: []expectedRel{
			{"Alex", "affiliated_with", "Google"},
			{"Alex", "affiliated_with", "Meta"},
		},
	},
	{
		name:    "LLM wraps response in markdown fence",
		message: "Meeting with Bob at Acme Corp",
		mockEntityResp: "```json\n[{\"name\":\"Bob\",\"type\":\"PERSON\",\"confidence\":0.95},{\"name\":\"Acme Corp\",\"type\":\"ORG\",\"confidence\":0.9}]\n```",
		mockRelResp: "```json\n[{\"subject\":\"Bob\",\"predicate\":\"affiliated_with\",\"object\":\"Acme Corp\",\"confidence\":0.9}]\n```",
		expectedEntities: map[string]graph.EntityType{
			"Bob":       graph.EntityPerson,
			"Acme Corp": graph.EntityOrg,
		},
		expectedRels: []expectedRel{
			{"Bob", "affiliated_with", "Acme Corp"},
		},
	},
	{
		name:    "duplicate entities deduplicated",
		message: "Sarah called Sarah's team at the office",
		mockEntityResp: `[
			{"name":"Sarah","type":"PERSON","confidence":0.95},
			{"name":"sarah","type":"PERSON","confidence":0.9}
		]`,
		mockRelResp: `[]`,
		expectedEntities: map[string]graph.EntityType{
			"Sarah": graph.EntityPerson,
		},
		expectedRels: nil,
	},
	{
		name:    "family relationships",
		message: "My brother David lives in San Francisco with his wife Maria",
		mockEntityResp: `[
			{"name":"David","type":"PERSON","confidence":0.95},
			{"name":"San Francisco","type":"GPE","confidence":0.99},
			{"name":"Maria","type":"PERSON","confidence":0.95}
		]`,
		mockRelResp: `[
			{"subject":"speaker","predicate":"kin_of","object":"David","confidence":0.95},
			{"subject":"David","predicate":"located_in","object":"San Francisco","confidence":0.9},
			{"subject":"David","predicate":"kin_of","object":"Maria","confidence":0.95}
		]`,
		expectedEntities: map[string]graph.EntityType{
			"David":         graph.EntityPerson,
			"San Francisco": graph.EntityGPE,
			"Maria":         graph.EntityPerson,
		},
		expectedRels: []expectedRel{
			{"speaker", "kin_of", "David"},
			{"David", "located_in", "San Francisco"},
			{"David", "kin_of", "Maria"},
		},
	},
	{
		name:    "backchannel with no entities",
		message: "ok sounds good, thanks!",
		mockEntityResp: `[]`,
		mockRelResp:    "",
		expectedEntities: map[string]graph.EntityType{},
		expectedRels: nil,
	},
	{
		name:    "short entity name filtered (<=2 chars)",
		message: "AI is changing everything at IBM",
		mockEntityResp: `[
			{"name":"AI","type":"PRODUCT","confidence":0.8},
			{"name":"IBM","type":"ORG","confidence":0.99}
		]`,
		mockRelResp: `[]`,
		expectedEntities: map[string]graph.EntityType{
			"IBM": graph.EntityOrg,
		},
		expectedRels: nil,
	},
	{
		name:    "misclassified email fixed by regex",
		message: "Contact me at test@foo.com for details",
		mockEntityResp: `[
			{"name":"test@foo.com","type":"PERSON","confidence":0.7}
		]`,
		mockRelResp: `[]`,
		expectedEntities: map[string]graph.EntityType{
			"test@foo.com": graph.EntityEmail,
		},
		expectedRels: nil,
	},
	{
		name:    "technology entities (software, frameworks, AI models)",
		message: "We migrated from Postgres to SQLite for storage. Claude handles the LLM layer.",
		mockEntityResp: `[
			{"name":"Postgres","type":"TECHNOLOGY","confidence":0.95},
			{"name":"SQLite","type":"TECHNOLOGY","confidence":0.95},
			{"name":"Claude","type":"TECHNOLOGY","confidence":0.9}
		]`,
		mockRelResp: `[]`,
		expectedEntities: map[string]graph.EntityType{
			"Postgres": graph.EntityTechnology,
			"SQLite":   graph.EntityTechnology,
			"Claude":   graph.EntityTechnology,
		},
		expectedRels: nil,
	},
	{
		name:    "technology vs product distinction",
		message: "Using React and TypeScript for the frontend. The iPhone app uses Swift.",
		mockEntityResp: `[
			{"name":"React","type":"TECHNOLOGY","confidence":0.95},
			{"name":"TypeScript","type":"TECHNOLOGY","confidence":0.95},
			{"name":"iPhone","type":"PRODUCT","confidence":0.9},
			{"name":"Swift","type":"TECHNOLOGY","confidence":0.9}
		]`,
		mockRelResp: `[]`,
		expectedEntities: map[string]graph.EntityType{
			"React":      graph.EntityTechnology,
			"TypeScript": graph.EntityTechnology,
			"iPhone":     graph.EntityProduct,
			"Swift":      graph.EntityTechnology,
		},
		expectedRels: nil,
	},
}

func TestSyntheticExtraction(t *testing.T) {
	for _, tc := range syntheticCases {
		t.Run(tc.name, func(t *testing.T) {
			var responses []string
			responses = append(responses, tc.mockEntityResp)
			if tc.mockRelResp != "" {
				responses = append(responses, tc.mockRelResp)
			}

			mock := &MockGenerator{responses: responses}
			extractor := NewDeepExtractor(mock)
			result, err := extractor.ExtractAll(tc.message)
			if err != nil {
				t.Fatalf("ExtractAll failed: %v", err)
			}

			// Check entity count
			if len(result.Entities) != len(tc.expectedEntities) {
				gotNames := make([]string, len(result.Entities))
				for i, e := range result.Entities {
					gotNames[i] = e.Name + ":" + string(e.Type)
				}
				t.Errorf("Expected %d entities, got %d: %v",
					len(tc.expectedEntities), len(result.Entities), gotNames)
			}

			// Check each expected entity is present with correct type
			gotEntities := make(map[string]graph.EntityType)
			for _, e := range result.Entities {
				gotEntities[e.Name] = e.Type
			}
			for name, expectedType := range tc.expectedEntities {
				gotType, found := gotEntities[name]
				if !found {
					t.Errorf("Expected entity %q not found in results", name)
				} else if gotType != expectedType {
					t.Errorf("Entity %q: expected type %s, got %s", name, expectedType, gotType)
				}
			}

			// Check no unexpected entities
			for _, e := range result.Entities {
				if _, expected := tc.expectedEntities[e.Name]; !expected {
					t.Errorf("Unexpected entity in results: %s (%s)", e.Name, e.Type)
				}
			}

			// Check relationships
			expectedRelCount := len(tc.expectedRels)
			if len(result.Relationships) != expectedRelCount {
				t.Errorf("Expected %d relationships, got %d: %+v",
					expectedRelCount, len(result.Relationships), result.Relationships)
			}

			for _, exp := range tc.expectedRels {
				found := false
				for _, got := range result.Relationships {
					if got.Subject == exp.subject && got.Predicate == exp.predicate && got.Object == exp.object {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("Expected relationship %s->%s->%s not found",
						exp.subject, exp.predicate, exp.object)
				}
			}
		})
	}
}

// TestPostProcessEntityFiltering tests the post-processing filters in isolation
// with various noise patterns seen in real chat.
func TestPostProcessEntityFiltering(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		input    []ExtractedEntity
		expected []string // expected entity names after filtering
	}{
		{
			name: "filters pronouns and noise words",
			text: "I think he should talk to them about it",
			input: []ExtractedEntity{
				{Name: "I", Type: graph.EntityPerson, Confidence: 0.5},
				{Name: "he", Type: graph.EntityPerson, Confidence: 0.5},
				{Name: "them", Type: graph.EntityPerson, Confidence: 0.3},
			},
			expected: nil,
		},
		{
			name: "filters generic product descriptions",
			text: "the new feature is working",
			input: []ExtractedEntity{
				{Name: "the new feature", Type: graph.EntityProduct, Confidence: 0.6},
				{Name: "the analytics platform", Type: graph.EntityProduct, Confidence: 0.7},
			},
			expected: nil,
		},
		{
			name: "keeps named products",
			text: "We use Slack and Notion daily",
			input: []ExtractedEntity{
				{Name: "Slack", Type: graph.EntityProduct, Confidence: 0.95},
				{Name: "Notion", Type: graph.EntityProduct, Confidence: 0.95},
			},
			expected: []string{"Slack", "Notion"},
		},
		{
			name: "regex catches emails LLM missed",
			text: "Email me at admin@test.org or support@test.org",
			input: []ExtractedEntity{},
			expected: []string{"admin@test.org", "support@test.org"},
		},
		{
			name: "regex catches money LLM missed",
			text: "The budget is $10,000 for Q1",
			input: []ExtractedEntity{},
			expected: []string{"$10,000"},
		},
		{
			name: "deduplicates case-insensitive",
			text: "Google and google are the same",
			input: []ExtractedEntity{
				{Name: "Google", Type: graph.EntityOrg, Confidence: 0.99},
				{Name: "google", Type: graph.EntityOrg, Confidence: 0.95},
			},
			expected: []string{"Google"},
		},
		{
			name: "fixes misclassified email",
			text: "Contact bob@co.com",
			input: []ExtractedEntity{
				{Name: "bob@co.com", Type: graph.EntityPerson, Confidence: 0.7},
			},
			expected: []string{"bob@co.com"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := postProcessEntityList(tt.text, tt.input)
			gotNames := make([]string, len(result))
			for i, e := range result {
				gotNames[i] = e.Name
			}

			if len(result) != len(tt.expected) {
				t.Errorf("Expected %d entities %v, got %d: %v",
					len(tt.expected), tt.expected, len(result), gotNames)
				return
			}

			for i, name := range tt.expected {
				if gotNames[i] != name {
					t.Errorf("Entity %d: expected %q, got %q", i, name, gotNames[i])
				}
			}

			// If the test expects an email entity, verify its type is EMAIL
			for _, e := range result {
				if emailRegex.MatchString(e.Name) && e.Type != graph.EntityEmail {
					t.Errorf("Email entity %q should have type EMAIL, got %s", e.Name, e.Type)
				}
			}
		})
	}
}
