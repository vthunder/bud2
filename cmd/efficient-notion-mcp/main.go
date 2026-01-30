// efficient-notion-mcp provides MCP tools for efficient Notion page sync.
//
// This is a wrapper that imports github.com/vthunder/efficient-notion-mcp/notion
// and exposes the same MCP tools (pull, push, diff, query, schema).
//
// IMPORTANT: When adding features to the notion package, you must also update
// the tool definitions and handlers here. See state/system/guides/repositories.md
// for the full MCP change workflow.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/joho/godotenv"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/vthunder/efficient-notion-mcp/notion"
)

func main() {
	// Load .env file - try executable's parent dir (repo root), then exe dir, then cwd
	envPaths := []string{".env"}
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		envPaths = append([]string{
			filepath.Join(filepath.Dir(exeDir), ".env"), // parent of bin/ = repo root
			filepath.Join(exeDir, ".env"),
		}, envPaths...)
	}
	for _, p := range envPaths {
		if _, err := os.Stat(p); err == nil {
			_ = godotenv.Load(p)
			break
		}
	}

	s := server.NewMCPServer(
		"efficient-notion-mcp",
		"1.0.0",
		server.WithToolCapabilities(true),
	)

	// Register tools
	s.AddTool(pullTool(), handlePull)
	s.AddTool(pushTool(), handlePush)
	s.AddTool(diffTool(), handleDiff)
	s.AddTool(queryTool(), handleQuery)
	s.AddTool(schemaTool(), handleSchema)
	s.AddTool(createTool(), handleCreate)

	// Run server
	if err := server.ServeStdio(s); err != nil {
		fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
		os.Exit(1)
	}
}

func pullTool() mcp.Tool {
	return mcp.NewTool("notion_pull",
		mcp.WithDescription("Pull a Notion page to a local markdown file. Fetches all blocks and comments, converts to markdown with frontmatter. If scope is provided, rewrites notion:// links to relative paths where local copies exist."),
		mcp.WithString("page_id",
			mcp.Required(),
			mcp.Description("Notion page ID (with or without dashes)"),
		),
		mcp.WithString("output_path",
			mcp.Description("Output path - can be a full file path (e.g., /path/to/README.md) or a directory. If directory, saves as <dir>/<title>.md. Default: /tmp/notion/<title>.md"),
		),
		mcp.WithString("scope",
			mcp.Description("Directory to scan for .md files with notion_id frontmatter. If provided, enables link rewriting between local files."),
		),
		mcp.WithBoolean("recursive",
			mcp.Description("Whether to scan scope directory recursively. Default: true"),
		),
	)
}

func handlePull(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, _ := req.Params.Arguments.(map[string]any)
	pageID, _ := args["page_id"].(string)
	outputPath, _ := args["output_path"].(string)
	scope, _ := args["scope"].(string)
	recursive := true // default
	if r, ok := args["recursive"].(bool); ok {
		recursive = r
	}

	if pageID == "" {
		return mcp.NewToolResultError("page_id is required"), nil
	}

	client, err := notion.NewClient()
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to create client: %v", err)), nil
	}

	result, err := client.PullPageWithScope(pageID, outputPath, scope, recursive)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to pull page: %v", err)), nil
	}

	output := fmt.Sprintf(
		"Pulled page '%s' to %s\n\nPage ID: %s\nContent length: %d characters",
		result.Title, result.FilePath, result.PageID, len(result.Markdown),
	)
	if result.RewrittenLinks > 0 {
		output += fmt.Sprintf("\nLinks rewritten: %d", result.RewrittenLinks)
	}
	if result.FilesUpdated > 0 {
		output += fmt.Sprintf("\nOther files updated: %d", result.FilesUpdated)
	}

	return mcp.NewToolResultText(output), nil
}

func pushTool() mcp.Tool {
	return mcp.NewTool("notion_push",
		mcp.WithDescription("Push a local markdown file to Notion. Uses efficient erase+replace (not block-by-block deletion). File must have notion_id in frontmatter. If scope is provided, converts relative .md links to notion:// links before pushing."),
		mcp.WithString("file_path",
			mcp.Required(),
			mcp.Description("Path to the local markdown file (must have notion_id in frontmatter)"),
		),
		mcp.WithString("scope",
			mcp.Description("Directory to scan for .md files with notion_id frontmatter. If provided, enables link rewriting from relative paths to notion:// links."),
		),
		mcp.WithBoolean("recursive",
			mcp.Description("Whether to scan scope directory recursively. Default: true"),
		),
	)
}

func handlePush(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, _ := req.Params.Arguments.(map[string]any)
	filePath, _ := args["file_path"].(string)
	scope, _ := args["scope"].(string)
	recursive := true // default
	if r, ok := args["recursive"].(bool); ok {
		recursive = r
	}

	if filePath == "" {
		return mcp.NewToolResultError("file_path is required"), nil
	}

	client, err := notion.NewClient()
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to create client: %v", err)), nil
	}

	if err := client.PushPageWithScope(filePath, scope, recursive); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to push page: %v", err)), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf("Successfully pushed %s to Notion", filePath)), nil
}

func diffTool() mcp.Tool {
	return mcp.NewTool("notion_diff",
		mcp.WithDescription("Compare a local markdown file against its Notion page. Shows what would change if pushed."),
		mcp.WithString("file_path",
			mcp.Required(),
			mcp.Description("Path to the local markdown file (must have notion_id in frontmatter)"),
		),
	)
}

func handleDiff(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, _ := req.Params.Arguments.(map[string]any)
	filePath, _ := args["file_path"].(string)

	if filePath == "" {
		return mcp.NewToolResultError("file_path is required"), nil
	}

	client, err := notion.NewClient()
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to create client: %v", err)), nil
	}

	diff, err := client.DiffPage(filePath)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to diff page: %v", err)), nil
	}

	return mcp.NewToolResultText(diff), nil
}

func queryTool() mcp.Tool {
	return mcp.NewTool("notion_query",
		mcp.WithDescription("Query a Notion database. Returns flattened JSON with property values extracted (not nested Notion format)."),
		mcp.WithString("database_id",
			mcp.Required(),
			mcp.Description("Notion database ID (with or without dashes)"),
		),
		mcp.WithObject("filter",
			mcp.Description("Notion filter object (e.g., {\"property\": \"Status\", \"status\": {\"equals\": \"Active\"}})"),
		),
		mcp.WithArray("sorts",
			mcp.Description("Array of sort objects (e.g., [{\"property\": \"Name\", \"direction\": \"ascending\"}])"),
		),
		mcp.WithNumber("limit",
			mcp.Description("Maximum results to return (1-100, default 100)"),
		),
	)
}

func handleQuery(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, _ := req.Params.Arguments.(map[string]any)
	databaseID, _ := args["database_id"].(string)

	if databaseID == "" {
		return mcp.NewToolResultError("database_id is required"), nil
	}

	var filter map[string]any
	if f, ok := args["filter"].(map[string]any); ok {
		filter = f
	}

	var sorts []map[string]any
	if s, ok := args["sorts"].([]any); ok {
		for _, item := range s {
			if m, ok := item.(map[string]any); ok {
				sorts = append(sorts, m)
			}
		}
	}

	limit := 100
	if l, ok := args["limit"].(float64); ok {
		limit = int(l)
	}

	client, err := notion.NewClient()
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to create client: %v", err)), nil
	}

	result, err := client.QueryDatabase(databaseID, filter, sorts, limit)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to query database: %v", err)), nil
	}

	output, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to marshal results: %v", err)), nil
	}

	return mcp.NewToolResultText(string(output)), nil
}

func schemaTool() mcp.Tool {
	return mcp.NewTool("notion_schema",
		mcp.WithDescription("Get the schema of a Notion database (property names and types)."),
		mcp.WithString("database_id",
			mcp.Required(),
			mcp.Description("Notion database ID (with or without dashes)"),
		),
	)
}

func handleSchema(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, _ := req.Params.Arguments.(map[string]any)
	databaseID, _ := args["database_id"].(string)

	if databaseID == "" {
		return mcp.NewToolResultError("database_id is required"), nil
	}

	client, err := notion.NewClient()
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to create client: %v", err)), nil
	}

	schema, err := client.GetSchema(databaseID)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to get schema: %v", err)), nil
	}

	output, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to marshal schema: %v", err)), nil
	}

	return mcp.NewToolResultText(string(output)), nil
}

func createTool() mcp.Tool {
	return mcp.NewTool("notion_create",
		mcp.WithDescription("Create a new Notion page under a parent page. Returns the new page ID."),
		mcp.WithString("parent_id",
			mcp.Required(),
			mcp.Description("Parent page ID to create the new page under"),
		),
		mcp.WithString("title",
			mcp.Required(),
			mcp.Description("Title for the new page"),
		),
	)
}

func handleCreate(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, _ := req.Params.Arguments.(map[string]any)
	parentID, _ := args["parent_id"].(string)
	title, _ := args["title"].(string)

	if parentID == "" {
		return mcp.NewToolResultError("parent_id is required"), nil
	}
	if title == "" {
		return mcp.NewToolResultError("title is required"), nil
	}

	// Normalize parent ID (remove dashes)
	parentID = strings.ReplaceAll(parentID, "-", "")

	apiKey := os.Getenv("NOTION_API_KEY")
	if apiKey == "" {
		return mcp.NewToolResultError("NOTION_API_KEY environment variable not set"), nil
	}

	// Build request body
	body := map[string]any{
		"parent": map[string]any{
			"page_id": parentID,
		},
		"properties": map[string]any{
			"title": map[string]any{
				"title": []map[string]any{
					{
						"type": "text",
						"text": map[string]any{
							"content": title,
						},
					},
				},
			},
		},
	}

	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to marshal request: %v", err)), nil
	}

	// Create request
	httpReq, err := http.NewRequestWithContext(ctx, "POST", "https://api.notion.com/v1/pages", bytes.NewReader(bodyBytes))
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to create request: %v", err)), nil
	}

	httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Notion-Version", "2022-06-28")

	// Execute request
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to execute request: %v", err)), nil
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		return mcp.NewToolResultError(fmt.Sprintf("Notion API error (%d): %s", resp.StatusCode, string(respBody))), nil
	}

	// Parse response to get page ID
	var result map[string]any
	if err := json.Unmarshal(respBody, &result); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to parse response: %v", err)), nil
	}

	pageID, _ := result["id"].(string)
	url, _ := result["url"].(string)

	return mcp.NewToolResultText(fmt.Sprintf("Created page '%s'\n\nPage ID: %s\nURL: %s", title, pageID, url)), nil
}
