package github

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
	graphqlURL = "https://api.github.com/graphql"
)

// Client is a GitHub API client for Projects v2
type Client struct {
	token      string
	org        string // Scoped to this organization
	httpClient *http.Client
}

// Config holds GitHub client configuration
type Config struct {
	Token string // GitHub personal access token with project scope
	Org   string // Organization name (e.g., "avail")
}

// NewClient creates a new GitHub client from environment variables
func NewClient() (*Client, error) {
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		return nil, fmt.Errorf("GITHUB_TOKEN not set")
	}

	org := os.Getenv("GITHUB_ORG")
	if org == "" {
		return nil, fmt.Errorf("GITHUB_ORG not set")
	}

	return NewClientWithConfig(Config{
		Token: token,
		Org:   org,
	})
}

// NewClientWithConfig creates a new client with explicit configuration
func NewClientWithConfig(cfg Config) (*Client, error) {
	if cfg.Token == "" {
		return nil, fmt.Errorf("token is required")
	}
	if cfg.Org == "" {
		return nil, fmt.Errorf("org is required")
	}

	return &Client{
		token: cfg.Token,
		org:   cfg.Org,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}, nil
}

// Org returns the configured organization
func (c *Client) Org() string {
	return c.org
}

// graphqlRequest sends a GraphQL query to GitHub
func (c *Client) graphqlRequest(query string, variables map[string]any) ([]byte, error) {
	body := map[string]any{
		"query": query,
	}
	if variables != nil {
		body["variables"] = variables
	}

	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequest("POST", graphqlURL, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.token)
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
		return nil, fmt.Errorf("github API error (%d): %s", resp.StatusCode, string(respBody))
	}

	// Check for GraphQL errors
	var result struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if json.Unmarshal(respBody, &result) == nil && len(result.Errors) > 0 {
		return nil, fmt.Errorf("graphql error: %s", result.Errors[0].Message)
	}

	return respBody, nil
}

// Project represents a GitHub Project v2
type Project struct {
	ID     string `json:"id"`
	Number int    `json:"number"`
	Title  string `json:"title"`
	URL    string `json:"url"`
	Closed bool   `json:"closed"`
}

// ProjectItem represents an item in a project (issue, PR, or draft)
type ProjectItem struct {
	ID          string            `json:"id"`
	Type        string            `json:"type"` // ISSUE, PULL_REQUEST, DRAFT_ISSUE
	Title       string            `json:"title"`
	URL         string            `json:"url,omitempty"`
	State       string            `json:"state,omitempty"` // OPEN, CLOSED, MERGED
	FieldValues map[string]string `json:"fields,omitempty"`
}

// ProjectField represents a field in a project
type ProjectField struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Type    string   `json:"type"` // TEXT, NUMBER, DATE, SINGLE_SELECT, ITERATION
	Options []string `json:"options,omitempty"`
}

// ListProjects returns all projects for the configured organization
func (c *Client) ListProjects() ([]Project, error) {
	query := `
		query($org: String!) {
			organization(login: $org) {
				projectsV2(first: 50) {
					nodes {
						id
						number
						title
						url
						closed
					}
				}
			}
		}`

	resp, err := c.graphqlRequest(query, map[string]any{"org": c.org})
	if err != nil {
		return nil, err
	}

	var result struct {
		Data struct {
			Organization struct {
				ProjectsV2 struct {
					Nodes []Project `json:"nodes"`
				} `json:"projectsV2"`
			} `json:"organization"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	return result.Data.Organization.ProjectsV2.Nodes, nil
}

// GetProject returns a project by number
func (c *Client) GetProject(number int) (*Project, error) {
	query := `
		query($org: String!, $number: Int!) {
			organization(login: $org) {
				projectV2(number: $number) {
					id
					number
					title
					url
					closed
				}
			}
		}`

	resp, err := c.graphqlRequest(query, map[string]any{
		"org":    c.org,
		"number": number,
	})
	if err != nil {
		return nil, err
	}

	var result struct {
		Data struct {
			Organization struct {
				ProjectV2 *Project `json:"projectV2"`
			} `json:"organization"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	if result.Data.Organization.ProjectV2 == nil {
		return nil, fmt.Errorf("project #%d not found", number)
	}

	return result.Data.Organization.ProjectV2, nil
}

// GetProjectFields returns the fields for a project
func (c *Client) GetProjectFields(projectNumber int) ([]ProjectField, error) {
	query := `
		query($org: String!, $number: Int!) {
			organization(login: $org) {
				projectV2(number: $number) {
					fields(first: 50) {
						nodes {
							... on ProjectV2Field {
								id
								name
								dataType
							}
							... on ProjectV2IterationField {
								id
								name
								dataType
							}
							... on ProjectV2SingleSelectField {
								id
								name
								dataType
								options {
									name
								}
							}
						}
					}
				}
			}
		}`

	resp, err := c.graphqlRequest(query, map[string]any{
		"org":    c.org,
		"number": projectNumber,
	})
	if err != nil {
		return nil, err
	}

	var result struct {
		Data struct {
			Organization struct {
				ProjectV2 struct {
					Fields struct {
						Nodes []struct {
							ID       string `json:"id"`
							Name     string `json:"name"`
							DataType string `json:"dataType"`
							Options  []struct {
								Name string `json:"name"`
							} `json:"options"`
						} `json:"nodes"`
					} `json:"fields"`
				} `json:"projectV2"`
			} `json:"organization"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	var fields []ProjectField
	for _, f := range result.Data.Organization.ProjectV2.Fields.Nodes {
		if f.Name == "" {
			continue // Skip empty nodes
		}
		field := ProjectField{
			ID:   f.ID,
			Name: f.Name,
			Type: f.DataType,
		}
		for _, opt := range f.Options {
			field.Options = append(field.Options, opt.Name)
		}
		fields = append(fields, field)
	}

	return fields, nil
}

// QueryItemsParams for querying project items
type QueryItemsParams struct {
	ProjectNumber int
	Status        string // Filter by status field value (e.g., "Backlog", "In Progress")
	Sprint        string // Filter by sprint name (e.g., "Sprint 65") or "current" for latest, "backlog" for no sprint
	TeamArea      string // Filter by Team / Area field (e.g., "SE", "Docs", "Nexus")
	Priority      string // Filter by Priority field (e.g., "P0", "P1", "P2")
	MaxItems      int    // Limit results (default 100)
}

// QueryItems returns items from a project with optional filtering
// Uses pagination to fetch all items when filtering is applied
func (c *Client) QueryItems(params QueryItemsParams) ([]ProjectItem, error) {
	if params.MaxItems == 0 {
		params.MaxItems = 100
	}

	// When filtering, we need to fetch more items to find matches
	// Use pagination to fetch up to 500 items when filtering
	needsFilter := params.Status != "" || params.Sprint != "" || params.TeamArea != "" || params.Priority != ""
	pageSize := params.MaxItems
	maxFetch := params.MaxItems
	if needsFilter {
		pageSize = 100 // Fetch in batches of 100
		maxFetch = 500 // But cap at 500 total to avoid excessive API calls
	}

	// Query items with their field values (with pagination support)
	// Use last/before to get newest items first (items are ordered by addition time)
	query := `
		query($org: String!, $number: Int!, $last: Int!, $before: String) {
			organization(login: $org) {
				projectV2(number: $number) {
					items(last: $last, before: $before) {
						pageInfo {
							hasPreviousPage
							startCursor
						}
						nodes {
							id
							type
							fieldValues(first: 20) {
								nodes {
									... on ProjectV2ItemFieldTextValue {
										field { ... on ProjectV2Field { name } }
										text
									}
									... on ProjectV2ItemFieldNumberValue {
										field { ... on ProjectV2Field { name } }
										number
									}
									... on ProjectV2ItemFieldDateValue {
										field { ... on ProjectV2Field { name } }
										date
									}
									... on ProjectV2ItemFieldSingleSelectValue {
										field { ... on ProjectV2SingleSelectField { name } }
										name
									}
									... on ProjectV2ItemFieldIterationValue {
										field { ... on ProjectV2IterationField { name } }
										title
									}
								}
							}
							content {
								... on Issue {
									title
									url
									state
								}
								... on PullRequest {
									title
									url
									state
								}
								... on DraftIssue {
									title
								}
							}
						}
					}
				}
			}
		}`

	var allItems []ProjectItem
	var cursor *string
	totalFetched := 0

	for {
		variables := map[string]any{
			"org":    c.org,
			"number": params.ProjectNumber,
			"last":   pageSize,
		}
		if cursor != nil {
			variables["before"] = *cursor
		}

		resp, err := c.graphqlRequest(query, variables)
		if err != nil {
			return nil, err
		}

		var result struct {
			Data struct {
				Organization struct {
					ProjectV2 struct {
						Items struct {
							PageInfo struct {
								HasPreviousPage bool   `json:"hasPreviousPage"`
								StartCursor     string `json:"startCursor"`
							} `json:"pageInfo"`
							Nodes []struct {
								ID          string `json:"id"`
								Type        string `json:"type"`
								FieldValues struct {
									Nodes []struct {
										Field struct {
											Name string `json:"name"`
										} `json:"field"`
										Text   string  `json:"text,omitempty"`
										Name   string  `json:"name,omitempty"`
										Title  string  `json:"title,omitempty"`
										Date   string  `json:"date,omitempty"`
										Number float64 `json:"number,omitempty"`
									} `json:"nodes"`
								} `json:"fieldValues"`
								Content struct {
									Title string `json:"title"`
									URL   string `json:"url"`
									State string `json:"state"`
								} `json:"content"`
							} `json:"nodes"`
						} `json:"items"`
					} `json:"projectV2"`
				} `json:"organization"`
			} `json:"data"`
		}
		if err := json.Unmarshal(resp, &result); err != nil {
			return nil, fmt.Errorf("parse response: %w", err)
		}

		for _, n := range result.Data.Organization.ProjectV2.Items.Nodes {
			item := ProjectItem{
				ID:          n.ID,
				Type:        n.Type,
				Title:       n.Content.Title,
				URL:         n.Content.URL,
				State:       n.Content.State,
				FieldValues: make(map[string]string),
			}

			// Extract field values
			for _, fv := range n.FieldValues.Nodes {
				fieldName := fv.Field.Name
				if fieldName == "" {
					continue
				}
				var value string
				switch {
				case fv.Text != "":
					value = fv.Text
				case fv.Name != "":
					value = fv.Name
				case fv.Title != "":
					value = fv.Title
				case fv.Date != "":
					value = fv.Date
				case fv.Number != 0:
					value = fmt.Sprintf("%.0f", fv.Number)
				}
				if value != "" {
					item.FieldValues[fieldName] = value
				}
			}

			// Apply status filter if specified
			if params.Status != "" {
				status, ok := item.FieldValues["Status"]
				if !ok || !strings.EqualFold(status, params.Status) {
					continue
				}
			}

			// Apply sprint filter if specified
			if params.Sprint != "" {
				sprint, hasSprint := item.FieldValues["Sprint"]
				switch strings.ToLower(params.Sprint) {
				case "backlog":
					// Backlog = no sprint assigned
					if hasSprint {
						continue
					}
				default:
					// Match specific sprint name (case-insensitive)
					if !hasSprint || !strings.EqualFold(sprint, params.Sprint) {
						continue
					}
				}
			}

			// Apply team/area filter if specified
			if params.TeamArea != "" {
				teamArea, ok := item.FieldValues["Team / Area"]
				if !ok || !strings.EqualFold(teamArea, params.TeamArea) {
					continue
				}
			}

			// Apply priority filter if specified
			if params.Priority != "" {
				priority, ok := item.FieldValues["Priority"]
				if !ok || !strings.EqualFold(priority, params.Priority) {
					continue
				}
			}

			allItems = append(allItems, item)

			// Check if we have enough items
			if len(allItems) >= params.MaxItems {
				return allItems[:params.MaxItems], nil
			}
		}

		totalFetched += len(result.Data.Organization.ProjectV2.Items.Nodes)

		// Stop if no more pages or we've fetched enough
		if !result.Data.Organization.ProjectV2.Items.PageInfo.HasPreviousPage {
			break
		}
		if totalFetched >= maxFetch {
			break
		}

		cursor = &result.Data.Organization.ProjectV2.Items.PageInfo.StartCursor
	}

	return allItems, nil
}

// CompactItem returns a one-line string representation of an item
// Format: "[Status] Title" or "[Status] Title (state)" for closed items
func (item *ProjectItem) CompactItem() string {
	var parts []string

	// Status field takes priority
	if status, ok := item.FieldValues["Status"]; ok {
		parts = append(parts, fmt.Sprintf("[%s]", status))
	}

	parts = append(parts, item.Title)

	// Add state for closed/merged items
	if item.State != "" && item.State != "OPEN" {
		parts = append(parts, fmt.Sprintf("(%s)", strings.ToLower(item.State)))
	}

	return strings.Join(parts, " ")
}

// FormatItemsCompact formats a slice of items in a compact, token-efficient format
func FormatItemsCompact(items []ProjectItem) string {
	if len(items) == 0 {
		return "No items."
	}

	var lines []string
	for _, item := range items {
		lines = append(lines, item.CompactItem())
	}
	return strings.Join(lines, "\n")
}
