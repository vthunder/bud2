package tools

import (
	"fmt"

	"github.com/vthunder/bud2/internal/mcp"
)

// RegisterResourceTools registers generic resource access tools.
func RegisterResourceTools(server *mcp.Server, deps *Dependencies) {
	server.RegisterTool("read_resource", mcp.ToolDef{
		Description: `Read an MCP resource by URI. Routes to the appropriate provider based on the URI scheme. Examples: "gk://guides/extraction", "gk://guides/query".`,
		Properties: map[string]mcp.PropDef{
			"uri":    {Type: "string", Description: "Full resource URI, e.g. gk://guides/extraction"},
			"domain": {Type: "string", Description: "Domain path for routing (e.g. / or /projects/foo). Defaults to / if omitted."},
		},
		Required: []string{"uri"},
	}, func(ctx any, args map[string]any) (string, error) {
		if deps.ReadResource == nil {
			return "", fmt.Errorf("read_resource: no resource provider configured")
		}
		uri, _ := args["uri"].(string)
		if uri == "" {
			return "", fmt.Errorf("read_resource: uri is required")
		}
		domain, _ := args["domain"].(string)
		if domain == "" {
			domain = "/"
		}
		content, err := deps.ReadResource(domain, uri)
		if err != nil {
			return "", fmt.Errorf("read_resource(%s): %w", uri, err)
		}
		return content, nil
	})
}
