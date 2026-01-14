// Package notion provides sync utilities for bidirectional Notion ↔ markdown sync.
package notion

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	notionAPIBase    = "https://api.notion.com/v1"
	notionAPIVersion = "2022-06-28"
)

// SyncClient handles direct Notion API operations for sync
type SyncClient struct {
	apiKey     string
	httpClient *http.Client
	userCache  map[string]string // user ID -> name cache
}

// NewSyncClient creates a new sync client using NOTION_API_KEY env var or .env file
func NewSyncClient() (*SyncClient, error) {
	apiKey := os.Getenv("NOTION_API_KEY")

	// If not in env, try loading from .env file
	if apiKey == "" {
		apiKey = loadFromEnvFile("NOTION_API_KEY")
	}

	if apiKey == "" {
		return nil, fmt.Errorf("NOTION_API_KEY not found in environment or .env file")
	}
	return &SyncClient{
		apiKey: apiKey,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		userCache: make(map[string]string),
	}, nil
}

// loadFromEnvFile reads a key from .env file in common locations
func loadFromEnvFile(key string) string {
	// Try common .env locations
	locations := []string{
		".env",
		"/Users/thunder/src/bud2/.env",
	}

	// Also check BUD_STATE_PATH parent
	if statePath := os.Getenv("BUD_STATE_PATH"); statePath != "" {
		locations = append(locations, filepath.Join(filepath.Dir(statePath), ".env"))
	}

	for _, loc := range locations {
		if val := readEnvKey(loc, key); val != "" {
			return val
		}
	}
	return ""
}

// readEnvKey reads a specific key from an env file
func readEnvKey(path, key string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}

	prefix := key + "="
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, prefix) {
			val := strings.TrimPrefix(line, prefix)
			// Remove quotes if present
			val = strings.Trim(val, `"'`)
			return val
		}
	}
	return ""
}

// Comment represents a Notion comment
type Comment struct {
	ID        string    `json:"id"`
	Author    string    `json:"author"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_time"`
	BlockID   string    `json:"block_id,omitempty"` // empty if page-level comment
}

// PullResult contains the result of pulling a page
type PullResult struct {
	Markdown string
	FilePath string
	PageID   string
	Title    string
}

// PullPage fetches a Notion page with comments and saves as markdown
func (c *SyncClient) PullPage(pageID string, outputDir string) (*PullResult, error) {
	// Normalize page ID (remove dashes if present)
	pageID = strings.ReplaceAll(pageID, "-", "")

	// Get page metadata for title
	title, err := c.getPageTitle(pageID)
	if err != nil {
		title = pageID // fallback to ID if can't get title
	}

	// Fetch all blocks
	blocks, err := c.fetchAllBlocks(pageID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch blocks: %w", err)
	}

	// Fetch comments for the page
	comments, err := c.fetchComments(pageID)
	if err != nil {
		// Non-fatal - continue without comments
		comments = nil
	}

	// Convert blocks to markdown
	markdown := BlocksToMarkdown(blocks)

	// Add comments as blockquotes at the end if any
	if len(comments) > 0 {
		markdown += "\n---\n\n## Comments\n\n"
		for _, comment := range comments {
			date := comment.CreatedAt.Format("Jan 2, 2006")
			markdown += fmt.Sprintf("> 💬 **%s** *(%s)*: %s\n\n", comment.Author, date, comment.Content)
		}
	}

	// Create output directory if needed
	if outputDir == "" {
		outputDir = "/tmp/notion"
	}
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create output dir: %w", err)
	}

	// Generate safe filename from title
	safeTitle := sanitizeFilename(title)
	filePath := filepath.Join(outputDir, safeTitle+".md")

	// Add frontmatter
	frontmatter := fmt.Sprintf("---\nnotion_id: %s\ntitle: %s\npulled_at: %s\n---\n\n",
		pageID, title, time.Now().Format(time.RFC3339))
	content := frontmatter + markdown

	// Write file
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		return nil, fmt.Errorf("failed to write file: %w", err)
	}

	return &PullResult{
		Markdown: content,
		FilePath: filePath,
		PageID:   pageID,
		Title:    title,
	}, nil
}

// PushPage reads a markdown file and pushes it to Notion (erase + replace)
func (c *SyncClient) PushPage(filePath string) error {
	// Read markdown file
	content, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read file: %w", err)
	}

	// Parse frontmatter to get page ID
	pageID, markdown := parseFrontmatter(string(content))
	if pageID == "" {
		return fmt.Errorf("no notion_id found in frontmatter")
	}

	// Extract and remove comments section (we'll preserve them)
	markdown, preservedComments := extractCommentsSection(markdown)

	// Convert markdown to blocks
	blocks := MarkdownToBlocks(markdown)

	// Add preserved comments back as blockquotes
	if preservedComments != "" {
		blocks = append(blocks, map[string]any{
			"object":  "block",
			"type":    "divider",
			"divider": map[string]any{},
		})
		blocks = append(blocks, map[string]any{
			"object": "block",
			"type":   "heading_2",
			"heading_2": map[string]any{
				"rich_text": []map[string]any{
					{"type": "text", "text": map[string]string{"content": "Comments"}},
				},
			},
		})
		// Parse preserved comments back into quote blocks
		commentBlocks := MarkdownToBlocks(preservedComments)
		blocks = append(blocks, commentBlocks...)
	}

	// Erase existing content
	if err := c.erasePage(pageID); err != nil {
		return fmt.Errorf("failed to erase page: %w", err)
	}

	// Append new blocks (in batches of 100)
	if err := c.appendBlocksBatched(pageID, blocks); err != nil {
		return fmt.Errorf("failed to append blocks: %w", err)
	}

	return nil
}

// DiffPage compares local markdown against current Notion content
func (c *SyncClient) DiffPage(filePath string) (string, error) {
	// Read local file
	localContent, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to read file: %w", err)
	}

	// Parse frontmatter
	pageID, localMarkdown := parseFrontmatter(string(localContent))
	if pageID == "" {
		return "", fmt.Errorf("no notion_id found in frontmatter")
	}

	// Fetch current Notion content
	blocks, err := c.fetchAllBlocks(pageID)
	if err != nil {
		return "", fmt.Errorf("failed to fetch blocks: %w", err)
	}

	notionMarkdown := BlocksToMarkdown(blocks)

	// Simple line-by-line diff
	localLines := strings.Split(strings.TrimSpace(localMarkdown), "\n")
	notionLines := strings.Split(strings.TrimSpace(notionMarkdown), "\n")

	var diff strings.Builder
	diff.WriteString(fmt.Sprintf("Comparing %s against Notion page %s\n\n", filePath, pageID))

	// Very simple diff - show lines that differ
	maxLines := len(localLines)
	if len(notionLines) > maxLines {
		maxLines = len(notionLines)
	}

	hasChanges := false
	for i := 0; i < maxLines; i++ {
		var local, notion string
		if i < len(localLines) {
			local = localLines[i]
		}
		if i < len(notionLines) {
			notion = notionLines[i]
		}

		if local != notion {
			hasChanges = true
			if notion != "" {
				diff.WriteString(fmt.Sprintf("- %s\n", notion))
			}
			if local != "" {
				diff.WriteString(fmt.Sprintf("+ %s\n", local))
			}
		}
	}

	if !hasChanges {
		return "No changes detected.", nil
	}

	return diff.String(), nil
}

// fetchAllBlocks recursively fetches all blocks from a page, including block-level comments
func (c *SyncClient) fetchAllBlocks(blockID string) ([]map[string]any, error) {
	var allBlocks []map[string]any
	cursor := ""

	for {
		url := fmt.Sprintf("%s/blocks/%s/children?page_size=100", notionAPIBase, blockID)
		if cursor != "" {
			url += "&start_cursor=" + cursor
		}

		resp, err := c.doRequest("GET", url, nil)
		if err != nil {
			return nil, err
		}

		var result struct {
			Results    []map[string]any `json:"results"`
			HasMore    bool             `json:"has_more"`
			NextCursor string           `json:"next_cursor"`
		}
		if err := json.Unmarshal(resp, &result); err != nil {
			return nil, fmt.Errorf("failed to parse response: %w", err)
		}

		// Process blocks: fetch children and comments
		for i, block := range result.Results {
			blockID, _ := block["id"].(string)
			if blockID == "" {
				continue
			}

			// Fetch comments for this block
			if comments, err := c.fetchComments(blockID); err == nil && len(comments) > 0 {
				// Convert comments to serializable format
				var commentsData []map[string]any
				for _, comment := range comments {
					commentsData = append(commentsData, map[string]any{
						"author":     comment.Author,
						"content":    comment.Content,
						"created_at": comment.CreatedAt.Format("Jan 2, 2006"),
					})
				}
				result.Results[i]["_comments"] = commentsData
			}

			// Fetch children for blocks that have them
			hasChildren, _ := block["has_children"].(bool)
			if hasChildren {
				children, err := c.fetchBlockChildren(blockID)
				if err == nil && len(children) > 0 {
					// Inject children into the block based on type
					blockType, _ := block["type"].(string)
					if blockData, ok := block[blockType].(map[string]any); ok {
						blockData["children"] = children
						result.Results[i][blockType] = blockData
					}
				}
			}
		}

		allBlocks = append(allBlocks, result.Results...)

		if !result.HasMore {
			break
		}
		cursor = result.NextCursor
	}

	return allBlocks, nil
}

// fetchBlockChildren fetches immediate children of a block, including comments for each child
func (c *SyncClient) fetchBlockChildren(blockID string) ([]any, error) {
	var allChildren []any
	cursor := ""

	for {
		url := fmt.Sprintf("%s/blocks/%s/children?page_size=100", notionAPIBase, blockID)
		if cursor != "" {
			url += "&start_cursor=" + cursor
		}

		resp, err := c.doRequest("GET", url, nil)
		if err != nil {
			return nil, err
		}

		var result struct {
			Results    []map[string]any `json:"results"`
			HasMore    bool             `json:"has_more"`
			NextCursor string           `json:"next_cursor"`
		}
		if err := json.Unmarshal(resp, &result); err != nil {
			return nil, err
		}

		// Fetch comments for each child block
		for i, child := range result.Results {
			childID, _ := child["id"].(string)
			if childID == "" {
				continue
			}

			if comments, err := c.fetchComments(childID); err == nil && len(comments) > 0 {
				var commentsData []map[string]any
				for _, comment := range comments {
					commentsData = append(commentsData, map[string]any{
						"author":     comment.Author,
						"content":    comment.Content,
						"created_at": comment.CreatedAt.Format("Jan 2, 2006"),
					})
				}
				result.Results[i]["_comments"] = commentsData
			}
		}

		// Convert to []any for compatibility
		for _, r := range result.Results {
			allChildren = append(allChildren, r)
		}

		if !result.HasMore {
			break
		}
		cursor = result.NextCursor
	}

	return allChildren, nil
}

// fetchComments fetches all comments for a page or block
func (c *SyncClient) fetchComments(blockID string) ([]Comment, error) {
	url := fmt.Sprintf("%s/comments?block_id=%s&page_size=100", notionAPIBase, blockID)

	resp, err := c.doRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	var result struct {
		Results []struct {
			ID          string `json:"id"`
			CreatedTime string `json:"created_time"`
			CreatedBy   struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"created_by"`
			RichText []struct {
				PlainText string `json:"plain_text"`
			} `json:"rich_text"`
			Parent struct {
				BlockID string `json:"block_id"`
			} `json:"parent"`
		} `json:"results"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("failed to parse comments: %w", err)
	}

	var comments []Comment
	for _, r := range result.Results {
		var content strings.Builder
		for _, rt := range r.RichText {
			content.WriteString(rt.PlainText)
		}

		// Resolve author name: use inline name if available, otherwise fetch from API
		authorName := r.CreatedBy.Name
		if authorName == "" && r.CreatedBy.ID != "" {
			authorName = c.resolveUserName(r.CreatedBy.ID)
		}

		createdAt, _ := time.Parse(time.RFC3339, r.CreatedTime)
		comments = append(comments, Comment{
			ID:        r.ID,
			Author:    authorName,
			Content:   content.String(),
			CreatedAt: createdAt,
			BlockID:   r.Parent.BlockID,
		})
	}

	return comments, nil
}

// resolveUserName fetches and caches user name by ID
func (c *SyncClient) resolveUserName(userID string) string {
	// Check cache first
	if name, ok := c.userCache[userID]; ok {
		return name
	}

	// Fetch from API
	url := fmt.Sprintf("%s/users/%s", notionAPIBase, userID)
	resp, err := c.doRequest("GET", url, nil)
	if err != nil {
		c.userCache[userID] = "Unknown"
		return "Unknown"
	}

	var user struct {
		Name string `json:"name"`
		Type string `json:"type"`
		Bot  struct {
			Owner struct {
				Type string `json:"type"`
				User struct {
					Name string `json:"name"`
				} `json:"user"`
			} `json:"owner"`
		} `json:"bot"`
	}
	if err := json.Unmarshal(resp, &user); err != nil {
		c.userCache[userID] = "Unknown"
		return "Unknown"
	}

	name := user.Name
	if name == "" {
		name = "Unknown"
	}
	c.userCache[userID] = name
	return name
}

// getPageTitle fetches the title of a page
func (c *SyncClient) getPageTitle(pageID string) (string, error) {
	url := fmt.Sprintf("%s/pages/%s", notionAPIBase, pageID)

	resp, err := c.doRequest("GET", url, nil)
	if err != nil {
		return "", err
	}

	var result struct {
		Properties map[string]struct {
			Title []struct {
				PlainText string `json:"plain_text"`
			} `json:"title"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return "", err
	}

	// Look for title property
	for _, prop := range result.Properties {
		if len(prop.Title) > 0 {
			return prop.Title[0].PlainText, nil
		}
	}

	return "", fmt.Errorf("no title found")
}

// erasePage clears all content from a page using PATCH with erase_content=true
func (c *SyncClient) erasePage(pageID string) error {
	url := fmt.Sprintf("%s/pages/%s", notionAPIBase, pageID)
	body := map[string]any{
		"erase_content": true,
	}

	_, err := c.doRequest("PATCH", url, body)
	return err
}

// appendBlocksBatched appends blocks in batches of 100
func (c *SyncClient) appendBlocksBatched(pageID string, blocks []map[string]any) error {
	const batchSize = 100

	for i := 0; i < len(blocks); i += batchSize {
		end := i + batchSize
		if end > len(blocks) {
			end = len(blocks)
		}

		batch := blocks[i:end]
		body := map[string]any{
			"children": batch,
		}

		url := fmt.Sprintf("%s/blocks/%s/children", notionAPIBase, pageID)
		if _, err := c.doRequest("PATCH", url, body); err != nil {
			return fmt.Errorf("failed to append batch %d: %w", i/batchSize, err)
		}

		// Small delay to avoid rate limiting
		if end < len(blocks) {
			time.Sleep(100 * time.Millisecond)
		}
	}

	return nil
}

// doRequest makes an authenticated request to Notion API
func (c *SyncClient) doRequest(method, url string, body any) ([]byte, error) {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal body: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Notion-Version", notionAPIVersion)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

// Helper functions

func sanitizeFilename(name string) string {
	// Replace unsafe characters
	unsafe := regexp.MustCompile(`[<>:"/\\|?*]`)
	name = unsafe.ReplaceAllString(name, "_")
	// Limit length
	if len(name) > 100 {
		name = name[:100]
	}
	return name
}

func parseFrontmatter(content string) (pageID, markdown string) {
	if !strings.HasPrefix(content, "---\n") {
		return "", content
	}

	endIdx := strings.Index(content[4:], "\n---\n")
	if endIdx == -1 {
		return "", content
	}

	frontmatter := content[4 : 4+endIdx]
	markdown = content[4+endIdx+5:]

	// Extract notion_id
	for _, line := range strings.Split(frontmatter, "\n") {
		if strings.HasPrefix(line, "notion_id:") {
			pageID = strings.TrimSpace(strings.TrimPrefix(line, "notion_id:"))
			break
		}
	}

	return pageID, markdown
}

func extractCommentsSection(markdown string) (content, comments string) {
	// Look for "## Comments" section
	idx := strings.Index(markdown, "\n## Comments\n")
	if idx == -1 {
		return markdown, ""
	}

	// Find the divider before it
	dividerIdx := strings.LastIndex(markdown[:idx], "\n---\n")
	if dividerIdx != -1 {
		return strings.TrimSpace(markdown[:dividerIdx]), strings.TrimSpace(markdown[idx+13:])
	}

	return strings.TrimSpace(markdown[:idx]), strings.TrimSpace(markdown[idx+13:])
}
