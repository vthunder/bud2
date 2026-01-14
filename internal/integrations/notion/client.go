package notion

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	baseURL       = "https://api.notion.com/v1"
	notionVersion = "2022-06-28"
)

// Client is a Notion API client
type Client struct {
	token      string
	httpClient *http.Client
}

// NewClient creates a new Notion client from NOTION_API_KEY env var
func NewClient() (*Client, error) {
	token := os.Getenv("NOTION_API_KEY")
	if token == "" {
		return nil, fmt.Errorf("NOTION_API_KEY not set")
	}
	return &Client{
		token: token,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}, nil
}

// NewClientWithToken creates a client with explicit token
func NewClientWithToken(token string) *Client {
	return &Client{
		token: token,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// request makes an authenticated request to the Notion API
func (c *Client) request(method, path string, body any) ([]byte, error) {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal body: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, baseURL+path, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Notion-Version", notionVersion)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		var errResp ErrorResponse
		if json.Unmarshal(respBody, &errResp) == nil && errResp.Message != "" {
			return nil, fmt.Errorf("notion API error (%d): %s", resp.StatusCode, errResp.Message)
		}
		return nil, fmt.Errorf("notion API error (%d): %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

// ErrorResponse is a Notion API error
type ErrorResponse struct {
	Object  string `json:"object"`
	Status  int    `json:"status"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// SearchParams for the search endpoint
type SearchParams struct {
	Query       string       `json:"query,omitempty"`
	Filter      *SearchFilter `json:"filter,omitempty"`
	Sort        *SearchSort   `json:"sort,omitempty"`
	StartCursor string       `json:"start_cursor,omitempty"`
	PageSize    int          `json:"page_size,omitempty"`
}

type SearchFilter struct {
	Property string `json:"property"`
	Value    string `json:"value"` // "page" or "database"
}

type SearchSort struct {
	Direction string `json:"direction"` // "ascending" or "descending"
	Timestamp string `json:"timestamp"` // "last_edited_time"
}

// SearchResult is the response from search
type SearchResult struct {
	Object     string   `json:"object"`
	Results    []Object `json:"results"`
	NextCursor string   `json:"next_cursor,omitempty"`
	HasMore    bool     `json:"has_more"`
}

// Object is a generic Notion object (page or database)
type Object struct {
	Object         string                 `json:"object"` // "page" or "database"
	ID             string                 `json:"id"`
	CreatedTime    string                 `json:"created_time"`
	LastEditedTime string                 `json:"last_edited_time"`
	Title          []RichText             `json:"title,omitempty"`
	Properties     map[string]Property    `json:"properties,omitempty"`
	URL            string                 `json:"url,omitempty"`
	Parent         Parent                 `json:"parent,omitempty"`
}

// RichText is a Notion rich text object
type RichText struct {
	Type      string    `json:"type"`
	PlainText string    `json:"plain_text"`
	Text      *TextObj  `json:"text,omitempty"`
}

type TextObj struct {
	Content string `json:"content"`
}

// Parent describes the parent of an object
type Parent struct {
	Type       string `json:"type"`
	DatabaseID string `json:"database_id,omitempty"`
	PageID     string `json:"page_id,omitempty"`
	Workspace  bool   `json:"workspace,omitempty"`
}

// Property is a Notion property (simplified)
type Property struct {
	ID     string          `json:"id"`
	Type   string          `json:"type"`
	Name   string          `json:"name,omitempty"`
	Title  []RichText      `json:"title,omitempty"`
	RichText []RichText    `json:"rich_text,omitempty"`
	Number *float64        `json:"number,omitempty"`
	Select *SelectOption   `json:"select,omitempty"`
	MultiSelect []SelectOption `json:"multi_select,omitempty"`
	Date   *DateProperty   `json:"date,omitempty"`
	Checkbox bool          `json:"checkbox,omitempty"`
	URL    string          `json:"url,omitempty"`
	Status *SelectOption   `json:"status,omitempty"`
}

type SelectOption struct {
	ID    string `json:"id,omitempty"`
	Name  string `json:"name"`
	Color string `json:"color,omitempty"`
}

type DateProperty struct {
	Start string `json:"start"`
	End   string `json:"end,omitempty"`
}

// Search searches pages and databases
func (c *Client) Search(params SearchParams) (*SearchResult, error) {
	if params.PageSize == 0 {
		params.PageSize = 100
	}

	data, err := c.request("POST", "/search", params)
	if err != nil {
		return nil, err
	}

	var result SearchResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("unmarshal search result: %w", err)
	}

	return &result, nil
}

// GetPage retrieves a page by ID
func (c *Client) GetPage(pageID string) (*Object, error) {
	data, err := c.request("GET", "/pages/"+pageID, nil)
	if err != nil {
		return nil, err
	}

	var page Object
	if err := json.Unmarshal(data, &page); err != nil {
		return nil, fmt.Errorf("unmarshal page: %w", err)
	}

	return &page, nil
}

// GetDatabase retrieves a database schema by ID
func (c *Client) GetDatabase(databaseID string) (*Database, error) {
	data, err := c.request("GET", "/databases/"+databaseID, nil)
	if err != nil {
		return nil, err
	}

	var db Database
	if err := json.Unmarshal(data, &db); err != nil {
		return nil, fmt.Errorf("unmarshal database: %w", err)
	}

	return &db, nil
}

// Database is a Notion database with schema
type Database struct {
	Object         string                    `json:"object"`
	ID             string                    `json:"id"`
	Title          []RichText                `json:"title"`
	Properties     map[string]PropertySchema `json:"properties"`
	URL            string                    `json:"url"`
	CreatedTime    string                    `json:"created_time"`
	LastEditedTime string                    `json:"last_edited_time"`
}

// PropertySchema describes a database property
type PropertySchema struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Type        string         `json:"type"`
	Select      *SelectSchema  `json:"select,omitempty"`
	MultiSelect *SelectSchema  `json:"multi_select,omitempty"`
	Status      *StatusSchema  `json:"status,omitempty"`
}

type SelectSchema struct {
	Options []SelectOption `json:"options"`
}

type StatusSchema struct {
	Options []SelectOption `json:"options"`
	Groups  []StatusGroup  `json:"groups,omitempty"`
}

type StatusGroup struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Color     string   `json:"color"`
	OptionIDs []string `json:"option_ids"`
}

// QueryParams for querying a database
type QueryParams struct {
	Filter      any    `json:"filter,omitempty"`
	Sorts       []Sort `json:"sorts,omitempty"`
	StartCursor string `json:"start_cursor,omitempty"`
	PageSize    int    `json:"page_size,omitempty"`
}

type Sort struct {
	Property  string `json:"property,omitempty"`
	Timestamp string `json:"timestamp,omitempty"` // "created_time" or "last_edited_time"
	Direction string `json:"direction"`           // "ascending" or "descending"
}

// QueryResult is the response from querying a database
type QueryResult struct {
	Object     string   `json:"object"`
	Results    []Object `json:"results"`
	NextCursor string   `json:"next_cursor,omitempty"`
	HasMore    bool     `json:"has_more"`
}

// QueryDatabase queries a database with optional filter and sort
func (c *Client) QueryDatabase(databaseID string, params QueryParams) (*QueryResult, error) {
	if params.PageSize == 0 {
		params.PageSize = 100
	}

	data, err := c.request("POST", "/databases/"+databaseID+"/query", params)
	if err != nil {
		return nil, err
	}

	var result QueryResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("unmarshal query result: %w", err)
	}

	return &result, nil
}

// GetTitle extracts the plain text title from a page or database
func (o *Object) GetTitle() string {
	// Check title field (for databases)
	if len(o.Title) > 0 {
		return o.Title[0].PlainText
	}

	// Check properties for title type (for pages)
	for _, prop := range o.Properties {
		if prop.Type == "title" && len(prop.Title) > 0 {
			return prop.Title[0].PlainText
		}
	}

	return ""
}

// GetPropertyText gets the text value of a property
func (o *Object) GetPropertyText(name string) string {
	prop, ok := o.Properties[name]
	if !ok {
		return ""
	}

	switch prop.Type {
	case "title":
		if len(prop.Title) > 0 {
			return prop.Title[0].PlainText
		}
	case "rich_text":
		if len(prop.RichText) > 0 {
			return prop.RichText[0].PlainText
		}
	case "select":
		if prop.Select != nil {
			return prop.Select.Name
		}
	case "status":
		if prop.Status != nil {
			return prop.Status.Name
		}
	case "url":
		return prop.URL
	case "number":
		if prop.Number != nil {
			return fmt.Sprintf("%v", *prop.Number)
		}
	case "checkbox":
		return fmt.Sprintf("%v", prop.Checkbox)
	case "date":
		if prop.Date != nil {
			return prop.Date.Start
		}
	case "multi_select":
		var names []string
		for _, opt := range prop.MultiSelect {
			names = append(names, opt.Name)
		}
		if len(names) > 0 {
			return fmt.Sprintf("%v", names)
		}
	}

	return ""
}

// Block represents a Notion block (content element)
type Block struct {
	Object         string     `json:"object"`
	ID             string     `json:"id"`
	Type           string     `json:"type"`
	HasChildren    bool       `json:"has_children"`
	Paragraph      *TextBlock `json:"paragraph,omitempty"`
	Heading1       *TextBlock `json:"heading_1,omitempty"`
	Heading2       *TextBlock `json:"heading_2,omitempty"`
	Heading3       *TextBlock `json:"heading_3,omitempty"`
	BulletedList   *TextBlock `json:"bulleted_list_item,omitempty"`
	NumberedList   *TextBlock `json:"numbered_list_item,omitempty"`
	ToDo           *ToDoBlock `json:"to_do,omitempty"`
	Toggle         *TextBlock `json:"toggle,omitempty"`
	Quote          *TextBlock `json:"quote,omitempty"`
	Callout        *TextBlock `json:"callout,omitempty"`
	Code           *CodeBlock `json:"code,omitempty"`
	Divider        any        `json:"divider,omitempty"`
	TableOfContents any       `json:"table_of_contents,omitempty"`
}

type TextBlock struct {
	RichText []RichText `json:"rich_text"`
	Color    string     `json:"color,omitempty"`
}

type ToDoBlock struct {
	RichText []RichText `json:"rich_text"`
	Checked  bool       `json:"checked"`
}

type CodeBlock struct {
	RichText []RichText `json:"rich_text"`
	Language string     `json:"language"`
}

// BlocksResult is the response from retrieving block children
type BlocksResult struct {
	Object     string  `json:"object"`
	Results    []Block `json:"results"`
	NextCursor string  `json:"next_cursor,omitempty"`
	HasMore    bool    `json:"has_more"`
}

// GetBlocks retrieves the content blocks of a page or block
func (c *Client) GetBlocks(blockID string) (*BlocksResult, error) {
	data, err := c.request("GET", "/blocks/"+blockID+"/children?page_size=100", nil)
	if err != nil {
		return nil, err
	}

	var result BlocksResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("unmarshal blocks: %w", err)
	}

	return &result, nil
}

// GetAllBlocks retrieves all blocks with pagination and recursively fetches children
func (c *Client) GetAllBlocks(blockID string) ([]Block, error) {
	var allBlocks []Block
	cursor := ""

	for {
		path := "/blocks/" + blockID + "/children?page_size=100"
		if cursor != "" {
			path += "&start_cursor=" + cursor
		}

		data, err := c.request("GET", path, nil)
		if err != nil {
			return nil, err
		}

		var result BlocksResult
		if err := json.Unmarshal(data, &result); err != nil {
			return nil, fmt.Errorf("unmarshal blocks: %w", err)
		}

		for _, block := range result.Results {
			allBlocks = append(allBlocks, block)
			// Recursively get children if present
			if block.HasChildren {
				children, err := c.GetAllBlocks(block.ID)
				if err != nil {
					// Log but don't fail on child fetch errors
					continue
				}
				allBlocks = append(allBlocks, children...)
			}
		}

		if !result.HasMore {
			break
		}
		cursor = result.NextCursor
	}

	return allBlocks, nil
}

// ToText extracts plain text from a block
func (b *Block) ToText() string {
	var richText []RichText

	switch b.Type {
	case "paragraph":
		if b.Paragraph != nil {
			richText = b.Paragraph.RichText
		}
	case "heading_1":
		if b.Heading1 != nil {
			richText = b.Heading1.RichText
		}
	case "heading_2":
		if b.Heading2 != nil {
			richText = b.Heading2.RichText
		}
	case "heading_3":
		if b.Heading3 != nil {
			richText = b.Heading3.RichText
		}
	case "bulleted_list_item":
		if b.BulletedList != nil {
			richText = b.BulletedList.RichText
		}
	case "numbered_list_item":
		if b.NumberedList != nil {
			richText = b.NumberedList.RichText
		}
	case "to_do":
		if b.ToDo != nil {
			richText = b.ToDo.RichText
		}
	case "toggle":
		if b.Toggle != nil {
			richText = b.Toggle.RichText
		}
	case "quote":
		if b.Quote != nil {
			richText = b.Quote.RichText
		}
	case "callout":
		if b.Callout != nil {
			richText = b.Callout.RichText
		}
	case "code":
		if b.Code != nil {
			richText = b.Code.RichText
		}
	case "divider":
		return "---"
	default:
		return ""
	}

	var text string
	for _, rt := range richText {
		text += rt.PlainText
	}
	return text
}

// CreatePageParams for creating a new page
type CreatePageParams struct {
	ParentPageID string // Page ID to create under
	Title        string // Page title
}

// CreatePageResult contains the created page info
type CreatePageResult struct {
	ID  string `json:"id"`
	URL string `json:"url"`
}

// CreatePage creates a new page under a parent page
func (c *Client) CreatePage(params CreatePageParams) (*CreatePageResult, error) {
	body := map[string]any{
		"parent": map[string]string{
			"type":    "page_id",
			"page_id": params.ParentPageID,
		},
		"properties": map[string]any{
			"title": map[string]any{
				"title": []map[string]any{
					{
						"type": "text",
						"text": map[string]string{
							"content": params.Title,
						},
					},
				},
			},
		},
	}

	data, err := c.request("POST", "/pages", body)
	if err != nil {
		return nil, err
	}

	var result struct {
		ID  string `json:"id"`
		URL string `json:"url"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("unmarshal create page result: %w", err)
	}

	return &CreatePageResult{
		ID:  result.ID,
		URL: result.URL,
	}, nil
}

// AppendBlocks appends content blocks to a page
func (c *Client) AppendBlocks(pageID string, blocks []map[string]any) error {
	body := map[string]any{
		"children": blocks,
	}

	_, err := c.request("PATCH", "/blocks/"+pageID+"/children", body)
	return err
}

// parseInlineMarkdown converts markdown inline formatting to Notion rich_text array
// Supports: **bold**, *italic*, `code`, [text](url)
func parseInlineMarkdown(text string) []map[string]any {
	var result []map[string]any
	i := 0

	for i < len(text) {
		// Bold: **text**
		if i+1 < len(text) && text[i] == '*' && text[i+1] == '*' {
			end := findClosing(text, i+2, "**")
			if end > 0 {
				if i > 0 {
					// Add preceding text first (check if we have buffered text)
				}
				result = append(result, map[string]any{
					"type": "text",
					"text": map[string]string{"content": text[i+2 : end]},
					"annotations": map[string]bool{"bold": true},
				})
				i = end + 2
				continue
			}
		}

		// Italic: *text* (but not **)
		if text[i] == '*' && (i+1 >= len(text) || text[i+1] != '*') {
			end := findClosingSingle(text, i+1, '*')
			if end > 0 {
				result = append(result, map[string]any{
					"type": "text",
					"text": map[string]string{"content": text[i+1 : end]},
					"annotations": map[string]bool{"italic": true},
				})
				i = end + 1
				continue
			}
		}

		// Inline code: `text`
		if text[i] == '`' {
			end := findClosingSingle(text, i+1, '`')
			if end > 0 {
				result = append(result, map[string]any{
					"type": "text",
					"text": map[string]string{"content": text[i+1 : end]},
					"annotations": map[string]bool{"code": true},
				})
				i = end + 1
				continue
			}
		}

		// Link: [text](url)
		if text[i] == '[' {
			closeBracket := findClosingSingle(text, i+1, ']')
			if closeBracket > 0 && closeBracket+1 < len(text) && text[closeBracket+1] == '(' {
				closeParen := findClosingSingle(text, closeBracket+2, ')')
				if closeParen > 0 {
					linkText := text[i+1 : closeBracket]
					linkURL := text[closeBracket+2 : closeParen]
					result = append(result, map[string]any{
						"type": "text",
						"text": map[string]any{
							"content": linkText,
							"link":    map[string]string{"url": linkURL},
						},
					})
					i = closeParen + 1
					continue
				}
			}
		}

		// Regular text - accumulate until next special char
		start := i
		for i < len(text) && text[i] != '*' && text[i] != '`' && text[i] != '[' {
			i++
		}
		if i > start {
			result = append(result, map[string]any{
				"type": "text",
				"text": map[string]string{"content": text[start:i]},
			})
		}
	}

	if len(result) == 0 {
		return []map[string]any{
			{"type": "text", "text": map[string]string{"content": text}},
		}
	}
	return result
}

// findClosing finds the closing marker (e.g., "**") starting from pos
func findClosing(text string, pos int, marker string) int {
	for i := pos; i <= len(text)-len(marker); i++ {
		if text[i:i+len(marker)] == marker {
			return i
		}
	}
	return -1
}

// findClosingSingle finds the closing single character marker
func findClosingSingle(text string, pos int, marker byte) int {
	for i := pos; i < len(text); i++ {
		if text[i] == marker {
			return i
		}
	}
	return -1
}

// MarkdownToBlocks converts markdown text to Notion blocks
func MarkdownToBlocks(markdown string) []map[string]any {
	var blocks []map[string]any
	lines := splitLines(markdown)

	for i := 0; i < len(lines); i++ {
		line := lines[i]

		// Skip empty lines
		if line == "" {
			continue
		}

		// Heading 1
		if len(line) > 2 && line[0] == '#' && line[1] == ' ' {
			blocks = append(blocks, map[string]any{
				"object": "block",
				"type":   "heading_1",
				"heading_1": map[string]any{
					"rich_text": parseInlineMarkdown(line[2:]),
				},
			})
			continue
		}

		// Heading 2
		if len(line) > 3 && line[0:3] == "## " {
			blocks = append(blocks, map[string]any{
				"object": "block",
				"type":   "heading_2",
				"heading_2": map[string]any{
					"rich_text": parseInlineMarkdown(line[3:]),
				},
			})
			continue
		}

		// Heading 3
		if len(line) > 4 && line[0:4] == "### " {
			blocks = append(blocks, map[string]any{
				"object": "block",
				"type":   "heading_3",
				"heading_3": map[string]any{
					"rich_text": parseInlineMarkdown(line[4:]),
				},
			})
			continue
		}

		// Divider
		if line == "---" {
			blocks = append(blocks, map[string]any{
				"object":  "block",
				"type":    "divider",
				"divider": map[string]any{},
			})
			continue
		}

		// Checkbox (to_do)
		if len(line) > 5 && (line[0:5] == "- [ ]" || line[0:5] == "- [x]") {
			checked := line[3] == 'x'
			text := ""
			if len(line) > 6 {
				text = line[6:]
			}
			blocks = append(blocks, map[string]any{
				"object": "block",
				"type":   "to_do",
				"to_do": map[string]any{
					"rich_text": parseInlineMarkdown(text),
					"checked":   checked,
				},
			})
			continue
		}

		// Bullet list
		if len(line) > 2 && line[0:2] == "- " {
			blocks = append(blocks, map[string]any{
				"object": "block",
				"type":   "bulleted_list_item",
				"bulleted_list_item": map[string]any{
					"rich_text": parseInlineMarkdown(line[2:]),
				},
			})
			continue
		}

		// Numbered list (simple check for digit + ". ")
		if len(line) > 3 && line[0] >= '0' && line[0] <= '9' {
			dotIdx := -1
			for j := 1; j < len(line) && j < 4; j++ {
				if line[j] == '.' && j+1 < len(line) && line[j+1] == ' ' {
					dotIdx = j
					break
				}
				if line[j] < '0' || line[j] > '9' {
					break
				}
			}
			if dotIdx > 0 {
				blocks = append(blocks, map[string]any{
					"object": "block",
					"type":   "numbered_list_item",
					"numbered_list_item": map[string]any{
						"rich_text": parseInlineMarkdown(line[dotIdx+2:]),
					},
				})
				continue
			}
		}

		// Quote
		if len(line) > 2 && line[0:2] == "> " {
			blocks = append(blocks, map[string]any{
				"object": "block",
				"type":   "quote",
				"quote": map[string]any{
					"rich_text": parseInlineMarkdown(line[2:]),
				},
			})
			continue
		}

		// Table - collect all table lines and create a proper Notion table
		if len(line) > 0 && line[0] == '|' {
			tableRows := []string{line}
			// Collect remaining table lines
			for i+1 < len(lines) && len(lines[i+1]) > 0 && lines[i+1][0] == '|' {
				i++
				tableRows = append(tableRows, lines[i])
			}
			tableBlock := parseMarkdownTable(tableRows)
			if tableBlock != nil {
				blocks = append(blocks, tableBlock)
			}
			continue
		}

		// Default: paragraph
		blocks = append(blocks, map[string]any{
			"object": "block",
			"type":   "paragraph",
			"paragraph": map[string]any{
				"rich_text": parseInlineMarkdown(line),
			},
		})
	}

	return blocks
}

// parseMarkdownTable converts markdown table lines to a Notion table block
func parseMarkdownTable(rows []string) map[string]any {
	if len(rows) < 2 {
		return nil
	}

	// Parse cells from a markdown table row
	parseCells := func(row string) []string {
		// Remove leading/trailing pipes and split by |
		row = strings.TrimSpace(row)
		if len(row) > 0 && row[0] == '|' {
			row = row[1:]
		}
		if len(row) > 0 && row[len(row)-1] == '|' {
			row = row[:len(row)-1]
		}
		parts := strings.Split(row, "|")
		var cells []string
		for _, p := range parts {
			cells = append(cells, strings.TrimSpace(p))
		}
		return cells
	}

	// Check if a row is a separator (|---|---|)
	isSeparator := func(row string) bool {
		row = strings.TrimSpace(row)
		for _, c := range row {
			if c != '|' && c != '-' && c != ':' && c != ' ' {
				return false
			}
		}
		return strings.Contains(row, "-")
	}

	// Filter out separator rows and parse data rows
	var dataRows [][]string
	for _, row := range rows {
		if !isSeparator(row) {
			cells := parseCells(row)
			if len(cells) > 0 {
				dataRows = append(dataRows, cells)
			}
		}
	}

	if len(dataRows) == 0 {
		return nil
	}

	// Determine table width from first row
	tableWidth := len(dataRows[0])

	// Build table rows for Notion
	var tableRowBlocks []map[string]any
	for _, cells := range dataRows {
		// Ensure all rows have same width
		for len(cells) < tableWidth {
			cells = append(cells, "")
		}
		// Convert cells to Notion format (array of rich_text arrays)
		var notionCells [][]map[string]any
		for _, cell := range cells[:tableWidth] {
			notionCells = append(notionCells, parseInlineMarkdown(cell))
		}
		tableRowBlocks = append(tableRowBlocks, map[string]any{
			"object": "block",
			"type":   "table_row",
			"table_row": map[string]any{
				"cells": notionCells,
			},
		})
	}

	return map[string]any{
		"object": "block",
		"type":   "table",
		"table": map[string]any{
			"table_width":      tableWidth,
			"has_column_header": true,
			"has_row_header":    false,
			"children":         tableRowBlocks,
		},
	}
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

// BlocksToMarkdown converts blocks to markdown text
func BlocksToMarkdown(blocks []Block) string {
	var result string
	listNum := 1
	lastType := ""

	for _, b := range blocks {
		text := b.ToText()
		if text == "" && b.Type != "divider" {
			continue
		}

		// Reset list numbering when exiting numbered list
		if b.Type != "numbered_list_item" && lastType == "numbered_list_item" {
			listNum = 1
		}

		switch b.Type {
		case "heading_1":
			result += "# " + text + "\n\n"
		case "heading_2":
			result += "## " + text + "\n\n"
		case "heading_3":
			result += "### " + text + "\n\n"
		case "paragraph":
			result += text + "\n\n"
		case "bulleted_list_item":
			result += "- " + text + "\n"
		case "numbered_list_item":
			result += fmt.Sprintf("%d. %s\n", listNum, text)
			listNum++
		case "to_do":
			check := " "
			if b.ToDo != nil && b.ToDo.Checked {
				check = "x"
			}
			result += fmt.Sprintf("- [%s] %s\n", check, text)
		case "quote":
			result += "> " + text + "\n\n"
		case "callout":
			result += "> 💡 " + text + "\n\n"
		case "code":
			lang := ""
			if b.Code != nil {
				lang = b.Code.Language
			}
			result += "```" + lang + "\n" + text + "\n```\n\n"
		case "divider":
			result += "---\n\n"
		default:
			if text != "" {
				result += text + "\n\n"
			}
		}

		lastType = b.Type
	}

	return result
}
