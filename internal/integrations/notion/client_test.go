package notion

import (
	"encoding/json"
	"testing"
)

func TestObjectGetTitle(t *testing.T) {
	// Test page with title property
	page := &Object{
		Object: "page",
		Properties: map[string]Property{
			"Name": {
				Type: "title",
				Title: []RichText{
					{PlainText: "Test Page"},
				},
			},
		},
	}

	if title := page.GetTitle(); title != "Test Page" {
		t.Errorf("Expected 'Test Page', got '%s'", title)
	}

	// Test database with title field
	db := &Object{
		Object: "database",
		Title: []RichText{
			{PlainText: "Test Database"},
		},
	}

	if title := db.GetTitle(); title != "Test Database" {
		t.Errorf("Expected 'Test Database', got '%s'", title)
	}
}

func TestObjectGetPropertyText(t *testing.T) {
	page := &Object{
		Properties: map[string]Property{
			"Status": {
				Type:   "status",
				Status: &SelectOption{Name: "In Progress"},
			},
			"Priority": {
				Type:   "select",
				Select: &SelectOption{Name: "High"},
			},
			"URL": {
				Type: "url",
				URL:  "https://example.com",
			},
		},
	}

	tests := []struct {
		prop     string
		expected string
	}{
		{"Status", "In Progress"},
		{"Priority", "High"},
		{"URL", "https://example.com"},
		{"NonExistent", ""},
	}

	for _, tt := range tests {
		if got := page.GetPropertyText(tt.prop); got != tt.expected {
			t.Errorf("GetPropertyText(%s) = '%s', want '%s'", tt.prop, got, tt.expected)
		}
	}
}

func TestSearchResultUnmarshal(t *testing.T) {
	jsonData := `{
		"object": "list",
		"results": [
			{
				"object": "page",
				"id": "abc123",
				"properties": {
					"Name": {
						"type": "title",
						"title": [{"plain_text": "Test"}]
					}
				}
			}
		],
		"has_more": false
	}`

	var result SearchResult
	if err := json.Unmarshal([]byte(jsonData), &result); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if len(result.Results) != 1 {
		t.Fatalf("Expected 1 result, got %d", len(result.Results))
	}

	if result.Results[0].GetTitle() != "Test" {
		t.Errorf("Expected title 'Test', got '%s'", result.Results[0].GetTitle())
	}
}

func TestDatabaseSchemaUnmarshal(t *testing.T) {
	jsonData := `{
		"object": "database",
		"id": "db123",
		"title": [{"plain_text": "Projects"}],
		"properties": {
			"Status": {
				"id": "abc",
				"name": "Status",
				"type": "status",
				"status": {
					"options": [
						{"name": "Not started", "color": "default"},
						{"name": "In progress", "color": "blue"},
						{"name": "Done", "color": "green"}
					]
				}
			}
		}
	}`

	var db Database
	if err := json.Unmarshal([]byte(jsonData), &db); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if db.ID != "db123" {
		t.Errorf("Expected ID 'db123', got '%s'", db.ID)
	}

	statusProp, ok := db.Properties["Status"]
	if !ok {
		t.Fatal("Expected Status property")
	}

	if statusProp.Status == nil || len(statusProp.Status.Options) != 3 {
		t.Error("Expected 3 status options")
	}
}
