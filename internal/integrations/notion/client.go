package notion

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
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
