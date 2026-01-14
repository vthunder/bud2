package notion

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMarkdownToBlocks_Headings(t *testing.T) {
	md := "# Heading 1\n## Heading 2\n### Heading 3"
	blocks := MarkdownToBlocks(md)

	if len(blocks) != 3 {
		t.Fatalf("Expected 3 blocks, got %d", len(blocks))
	}

	types := []string{"heading_1", "heading_2", "heading_3"}
	for i, expected := range types {
		if blocks[i]["type"] != expected {
			t.Errorf("Block %d: expected type %s, got %s", i, expected, blocks[i]["type"])
		}
	}
}

func TestMarkdownToBlocks_Lists(t *testing.T) {
	md := "- Bullet 1\n- Bullet 2\n1. Number 1\n2. Number 2"
	blocks := MarkdownToBlocks(md)

	if len(blocks) != 4 {
		t.Fatalf("Expected 4 blocks, got %d", len(blocks))
	}

	if blocks[0]["type"] != "bulleted_list_item" {
		t.Errorf("Expected bulleted_list_item, got %s", blocks[0]["type"])
	}
	if blocks[2]["type"] != "numbered_list_item" {
		t.Errorf("Expected numbered_list_item, got %s", blocks[2]["type"])
	}
}

func TestMarkdownToBlocks_Todo(t *testing.T) {
	md := "- [ ] Unchecked\n- [x] Checked"
	blocks := MarkdownToBlocks(md)

	if len(blocks) != 2 {
		t.Fatalf("Expected 2 blocks, got %d", len(blocks))
	}

	todo1 := blocks[0]["to_do"].(map[string]any)
	if todo1["checked"].(bool) != false {
		t.Error("First todo should be unchecked")
	}

	todo2 := blocks[1]["to_do"].(map[string]any)
	if todo2["checked"].(bool) != true {
		t.Error("Second todo should be checked")
	}
}

func TestMarkdownToBlocks_InlineFormatting(t *testing.T) {
	md := "This has **bold** and *italic* and `code`"
	blocks := MarkdownToBlocks(md)

	if len(blocks) != 1 {
		t.Fatalf("Expected 1 block, got %d", len(blocks))
	}

	para := blocks[0]["paragraph"].(map[string]any)
	richText := para["rich_text"].([]map[string]any)

	// Should have multiple rich_text segments
	if len(richText) < 4 {
		t.Errorf("Expected at least 4 rich_text segments, got %d", len(richText))
	}

	// Check bold segment
	var foundBold, foundItalic, foundCode bool
	for _, rt := range richText {
		if ann, ok := rt["annotations"].(map[string]bool); ok {
			if ann["bold"] {
				foundBold = true
			}
			if ann["italic"] {
				foundItalic = true
			}
			if ann["code"] {
				foundCode = true
			}
		}
	}

	if !foundBold {
		t.Error("Expected to find bold text")
	}
	if !foundItalic {
		t.Error("Expected to find italic text")
	}
	if !foundCode {
		t.Error("Expected to find code text")
	}
}

func TestBlocksToMarkdown(t *testing.T) {
	blocks := []map[string]any{
		{
			"type": "heading_1",
			"heading_1": map[string]any{
				"rich_text": []any{
					map[string]any{"plain_text": "Title"},
				},
			},
		},
		{
			"type": "paragraph",
			"paragraph": map[string]any{
				"rich_text": []any{
					map[string]any{"plain_text": "Some text"},
				},
			},
		},
		{
			"type": "bulleted_list_item",
			"bulleted_list_item": map[string]any{
				"rich_text": []any{
					map[string]any{"plain_text": "Item 1"},
				},
			},
		},
	}

	md := BlocksToMarkdown(blocks)

	if !strings.Contains(md, "# Title") {
		t.Error("Expected '# Title' in output")
	}
	if !strings.Contains(md, "Some text") {
		t.Error("Expected 'Some text' in output")
	}
	if !strings.Contains(md, "- Item 1") {
		t.Error("Expected '- Item 1' in output")
	}
}

func TestCreateBlock(t *testing.T) {
	block := CreateBlock("paragraph", "Hello **world**")

	if block["type"] != "paragraph" {
		t.Errorf("Expected type paragraph, got %s", block["type"])
	}

	para := block["paragraph"].(map[string]any)
	richText := para["rich_text"].([]map[string]any)

	if len(richText) < 2 {
		t.Errorf("Expected at least 2 rich_text segments, got %d", len(richText))
	}
}

func TestCreateBlock_Unsupported(t *testing.T) {
	block := CreateBlock("unsupported_type", "content")
	if block != nil {
		t.Error("Expected nil for unsupported block type")
	}
}

func TestRoundTrip(t *testing.T) {
	original := "# Test\n\nParagraph text\n\n- Bullet 1\n- Bullet 2\n"

	blocks := MarkdownToBlocks(original)

	// Convert blocks to JSON then back (simulating API round-trip)
	jsonBytes, _ := json.Marshal(blocks)
	var parsed []map[string]any
	json.Unmarshal(jsonBytes, &parsed)

	result := BlocksToMarkdown(parsed)

	if !strings.Contains(result, "# Test") {
		t.Error("Round-trip lost heading")
	}
	if !strings.Contains(result, "Paragraph text") {
		t.Error("Round-trip lost paragraph")
	}
	if !strings.Contains(result, "- Bullet 1") {
		t.Error("Round-trip lost bullet")
	}
}
