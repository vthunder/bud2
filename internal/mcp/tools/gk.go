package tools

import (
	"fmt"

	"github.com/vthunder/bud2/internal/mcp"
)

// domainProp is the shared "domain" property added to every GK tool.
// The HTTP handler injects this from the session token when absent.
var domainProp = mcp.PropDef{
	Type:        "string",
	Description: `Optional. Domain path for the knowledge graph DB. "/" = root state DB, "/projects/foo" = project DB. Defaults to the session's registered domain (injected automatically by the MCP server).`,
}

func gkRequired(fields ...string) []string { return fields }

// RegisterGKTools registers all 27 GK knowledge-graph tools with the MCP server.
// Each tool adds an optional "domain" parameter for multi-DB routing.
// The GKTool flag causes the HTTP handler to inject the session's default domain
// when "domain" is absent from the arguments.
func RegisterGKTools(server *mcp.Server, deps *Dependencies) {
	// ── Tier 1: Build the graph ─────────────────────────────────────────────

	server.RegisterTool("gk_add_entities", mcp.ToolDef{
		GKTool:      true,
		Description: "Batch-add entities to the knowledge graph. Upserts on (name, type). Each entity: {name, type, properties?, confidence? (0-1), source?}.",
		Properties: map[string]mcp.PropDef{
			"entities": {Type: "array", Description: "Array of entity objects: [{name: string, type: string, properties?: object, confidence?: number, source?: string}]"},
			"domain":   domainProp,
		},
		Required: gkRequired("entities"),
	}, func(ctx any, args map[string]any) (string, error) {
		return gkForward(deps, "add_entities", args)
	})

	server.RegisterTool("gk_add_relationships", mcp.ToolDef{
		GKTool:      true,
		Description: "Batch-add typed relationships between entities.",
		Properties: map[string]mcp.PropDef{
			"relationships": {Type: "array", Description: "Array of relationship objects: [{from_name: string, to_name: string, type: string, properties?: object, confidence?: number, source?: string}]"},
			"domain":        domainProp,
		},
		Required: gkRequired("relationships"),
	}, func(ctx any, args map[string]any) (string, error) {
		return gkForward(deps, "add_relationships", args)
	})

	server.RegisterTool("gk_add_observations", mcp.ToolDef{
		GKTool:      true,
		Description: "Batch-add observations linked to entities. Each observation must reference at least one existing entity by name.",
		Properties: map[string]mcp.PropDef{
			"observations": {Type: "array", Description: "Array of observation objects: [{content: string, entity_names: string[], tier?: 'detail'|'summary'|'overview', confidence?: number, source?: string, metadata?: object}]"},
			"domain":       domainProp,
		},
		Required: gkRequired("observations"),
	}, func(ctx any, args map[string]any) (string, error) {
		return gkForward(deps, "add_observations", args)
	})

	server.RegisterTool("gk_add_chunked_observation", mcp.ToolDef{
		GKTool:      true,
		Description: "Add a long observation that gets automatically split at sentence boundaries. Chunks share a group ID in metadata.",
		Properties: map[string]mcp.PropDef{
			"content":        {Type: "string", Description: "The full text to chunk"},
			"entity_names":   {Type: "array", Description: "Array of entity name strings to link to"},
			"metadata":       {Type: "object", Description: "Optional metadata key-value pairs"},
			"confidence":     {Type: "number", Description: "Optional confidence score 0-1"},
			"source":         {Type: "string", Description: "Optional source identifier"},
			"max_chunk_size": {Type: "number", Description: "Max chars per chunk (default 2000)"},
			"domain":         domainProp,
		},
		Required: gkRequired("content", "entity_names"),
	}, func(ctx any, args map[string]any) (string, error) {
		return gkForward(deps, "add_chunked_observation", args)
	})

	server.RegisterTool("gk_update_entities", mcp.ToolDef{
		GKTool:      true,
		Description: "Batch-update entity properties, confidence, or staleness tier by name.",
		Properties: map[string]mcp.PropDef{
			"updates": {Type: "array", Description: "Array of update objects: [{name: string, type?: string, properties?: object, confidence?: number, staleness_tier?: 'detail'|'summary'|'overview'}]"},
			"domain":  domainProp,
		},
		Required: gkRequired("updates"),
	}, func(ctx any, args map[string]any) (string, error) {
		return gkForward(deps, "update_entities", args)
	})

	server.RegisterTool("gk_update_relationships", mcp.ToolDef{
		GKTool:      true,
		Description: "Batch-update relationship properties or confidence.",
		Properties: map[string]mcp.PropDef{
			"updates": {Type: "array", Description: "Array of update objects: [{from_name: string, to_name: string, type: string, properties?: object, confidence?: number}]"},
			"domain":  domainProp,
		},
		Required: gkRequired("updates"),
	}, func(ctx any, args map[string]any) (string, error) {
		return gkForward(deps, "update_relationships", args)
	})

	server.RegisterTool("gk_delete_entities", mcp.ToolDef{
		GKTool:      true,
		Description: "Delete entities by name with cascade (removes all relationships and observations linked to them).",
		Properties: map[string]mcp.PropDef{
			"names":  {Type: "array", Description: "Array of entity name strings to delete"},
			"domain": domainProp,
		},
		Required: gkRequired("names"),
	}, func(ctx any, args map[string]any) (string, error) {
		return gkForward(deps, "delete_entities", args)
	})

	server.RegisterTool("gk_merge_entities", mcp.ToolDef{
		GKTool:      true,
		Description: "Merge duplicate entities into one canonical entity. Relationships and observations are re-linked to the target.",
		Properties: map[string]mcp.PropDef{
			"source_names": {Type: "array", Description: "Entity names to merge (will be deleted after merge)"},
			"target_name":  {Type: "string", Description: "Name of the entity to merge into (must exist)"},
			"domain":       domainProp,
		},
		Required: gkRequired("source_names", "target_name"),
	}, func(ctx any, args map[string]any) (string, error) {
		return gkForward(deps, "merge_entities", args)
	})

	// ── Tier 2: Search ───────────────────────────────────────────────────────

	server.RegisterTool("gk_search_keyword", mcp.ToolDef{
		GKTool:      true,
		Description: "BM25 keyword search over entity names, types, and observation content. Best for exact terms, proper nouns, and names.",
		Properties: map[string]mcp.PropDef{
			"query":       {Type: "string", Description: "Keyword query string"},
			"limit":       {Type: "number", Description: "Max results (default 10)"},
			"entity_type": {Type: "string", Description: "Filter by entity type"},
			"domain":      domainProp,
		},
		Required: gkRequired("query"),
	}, func(ctx any, args map[string]any) (string, error) {
		return gkForward(deps, "search_keyword", args)
	})

	server.RegisterTool("gk_search", mcp.ToolDef{
		GKTool:      true,
		Description: "Hybrid search combining BM25 keyword relevance, semantic similarity (Ollama embeddings), and temporal scoring. Use this as the default search when unsure.",
		Properties: map[string]mcp.PropDef{
			"query":       {Type: "string", Description: "Search query"},
			"limit":       {Type: "number", Description: "Max results (default 10)"},
			"entity_type": {Type: "string", Description: "Optional entity type filter"},
			"domain":      domainProp,
		},
		Required: gkRequired("query"),
	}, func(ctx any, args map[string]any) (string, error) {
		return gkForward(deps, "search", args)
	})

	server.RegisterTool("gk_search_entities", mcp.ToolDef{
		GKTool:      true,
		Description: "Find entities by name (fuzzy match). Returns entity IDs and metadata without full observations.",
		Properties: map[string]mcp.PropDef{
			"query":  {Type: "string", Description: "Entity name to search for"},
			"limit":  {Type: "number", Description: "Max results (default 10)"},
			"domain": domainProp,
		},
		Required: gkRequired("query"),
	}, func(ctx any, args map[string]any) (string, error) {
		return gkForward(deps, "search_entities", args)
	})

	server.RegisterTool("gk_list_entities", mcp.ToolDef{
		GKTool:      true,
		Description: "List entities by type without FTS. Good for enumerating all entities of a given type.",
		Properties: map[string]mcp.PropDef{
			"entity_type": {Type: "string", Description: "Entity type to list"},
			"limit":       {Type: "number", Description: "Max results (default 50)"},
			"offset":      {Type: "number", Description: "Pagination offset"},
			"domain":      domainProp,
		},
	}, func(ctx any, args map[string]any) (string, error) {
		return gkForward(deps, "list_entities", args)
	})

	server.RegisterTool("gk_read_observation", mcp.ToolDef{
		GKTool:      true,
		Description: "Read full observation text by ID. Use after search to retrieve complete content (search returns summaries).",
		Properties: map[string]mcp.PropDef{
			"id":     {Type: "string", Description: "Observation ID from search results"},
			"domain": domainProp,
		},
		Required: gkRequired("id"),
	}, func(ctx any, args map[string]any) (string, error) {
		return gkForward(deps, "read_observation", args)
	})

	// ── Tier 3: Navigate & analyze ───────────────────────────────────────────

	server.RegisterTool("gk_get_entity", mcp.ToolDef{
		GKTool:      true,
		Description: "Get full entity profile including all relationships and observation summaries.",
		Properties: map[string]mcp.PropDef{
			"name":   {Type: "string", Description: "Entity name"},
			"domain": domainProp,
		},
		Required: gkRequired("name"),
	}, func(ctx any, args map[string]any) (string, error) {
		return gkForward(deps, "get_entity", args)
	})

	server.RegisterTool("gk_get_entity_profile", mcp.ToolDef{
		GKTool:      true,
		Description: "Rich entity profile with truncated observations and relationship strength scores.",
		Properties: map[string]mcp.PropDef{
			"name":   {Type: "string", Description: "Entity name"},
			"domain": domainProp,
		},
		Required: gkRequired("name"),
	}, func(ctx any, args map[string]any) (string, error) {
		return gkForward(deps, "get_entity_profile", args)
	})

	server.RegisterTool("gk_get_relationships", mcp.ToolDef{
		GKTool:      true,
		Description: "Query relationships by entity name and/or relationship type.",
		Properties: map[string]mcp.PropDef{
			"entity_name": {Type: "string", Description: "Entity to get relationships for"},
			"type":        {Type: "string", Description: "Optional relationship type filter"},
			"direction":   {Type: "string", Description: "Optional: 'outgoing', 'incoming', or 'both' (default)"},
			"limit":       {Type: "number", Description: "Max results (default 50)"},
			"domain":      domainProp,
		},
	}, func(ctx any, args map[string]any) (string, error) {
		return gkForward(deps, "get_relationships", args)
	})

	server.RegisterTool("gk_list_entity_types", mcp.ToolDef{
		GKTool:      true,
		Description: "List all entity types in the graph with counts. Useful for understanding graph structure.",
		Properties: map[string]mcp.PropDef{
			"domain": domainProp,
		},
	}, func(ctx any, args map[string]any) (string, error) {
		return gkForward(deps, "list_entity_types", args)
	})

	server.RegisterTool("gk_find_paths", mcp.ToolDef{
		GKTool:      true,
		Description: "Find shortest paths between two entities in the graph.",
		Properties: map[string]mcp.PropDef{
			"from_name": {Type: "string", Description: "Source entity name"},
			"to_name":   {Type: "string", Description: "Target entity name"},
			"max_depth": {Type: "number", Description: "Max path length (default 5)"},
			"domain":    domainProp,
		},
		Required: gkRequired("from_name", "to_name"),
	}, func(ctx any, args map[string]any) (string, error) {
		return gkForward(deps, "find_paths", args)
	})

	server.RegisterTool("gk_get_neighbors", mcp.ToolDef{
		GKTool:      true,
		Description: "Multi-hop neighborhood exploration starting from an entity.",
		Properties: map[string]mcp.PropDef{
			"name":      {Type: "string", Description: "Starting entity name"},
			"max_depth": {Type: "number", Description: "Hops to traverse (default 2)"},
			"domain":    domainProp,
		},
		Required: gkRequired("name"),
	}, func(ctx any, args map[string]any) (string, error) {
		return gkForward(deps, "get_neighbors", args)
	})

	server.RegisterTool("gk_extract_subgraph", mcp.ToolDef{
		GKTool:      true,
		Description: "Extract a connected subgraph neighborhood around seed entities.",
		Properties: map[string]mcp.PropDef{
			"entity_names": {Type: "array", Description: "Seed entity names (strings)"},
			"max_depth":    {Type: "number", Description: "Hops from seeds (default 2)"},
			"domain":       domainProp,
		},
		Required: gkRequired("entity_names"),
	}, func(ctx any, args map[string]any) (string, error) {
		return gkForward(deps, "extract_subgraph", args)
	})

	server.RegisterTool("gk_get_centrality", mcp.ToolDef{
		GKTool:      true,
		Description: "Compute centrality scores (degree or PageRank) to find the most important entities.",
		Properties: map[string]mcp.PropDef{
			"algorithm":   {Type: "string", Description: "'degree' or 'pagerank' (default 'degree')"},
			"limit":       {Type: "number", Description: "Top N results (default 20)"},
			"entity_type": {Type: "string", Description: "Optional entity type filter"},
			"domain":      domainProp,
		},
	}, func(ctx any, args map[string]any) (string, error) {
		return gkForward(deps, "get_centrality", args)
	})

	server.RegisterTool("gk_get_timeline", mcp.ToolDef{
		GKTool:      true,
		Description: "Chronological observation history for an entity. Shows how knowledge accumulated over time.",
		Properties: map[string]mcp.PropDef{
			"entity_name": {Type: "string", Description: "Entity name"},
			"limit":       {Type: "number", Description: "Max entries (default 20)"},
			"domain":      domainProp,
		},
		Required: gkRequired("entity_name"),
	}, func(ctx any, args map[string]any) (string, error) {
		return gkForward(deps, "get_timeline", args)
	})

	server.RegisterTool("gk_validate_graph", mcp.ToolDef{
		GKTool:      true,
		Description: "Quality checks: find isolated entities, orphaned observations, duplicate candidates, and broken relationships.",
		Properties: map[string]mcp.PropDef{
			"domain": domainProp,
		},
	}, func(ctx any, args map[string]any) (string, error) {
		return gkForward(deps, "validate_graph", args)
	})

	// ── Tier 4: Maintenance ──────────────────────────────────────────────────

	server.RegisterTool("gk_get_stats", mcp.ToolDef{
		GKTool:      true,
		Description: "Aggregate graph statistics: entity counts by type, relationship counts, observation counts, DB size.",
		Properties: map[string]mcp.PropDef{
			"domain": domainProp,
		},
	}, func(ctx any, args map[string]any) (string, error) {
		return gkForward(deps, "get_stats", args)
	})

	server.RegisterTool("gk_prune_stale", mcp.ToolDef{
		GKTool:      true,
		Description: "Find and optionally remove entities with decayed temporal scores below threshold.",
		Properties: map[string]mcp.PropDef{
			"threshold": {Type: "number", Description: "Temporal score threshold (0-1, default 0.1)"},
			"dry_run":   {Type: "boolean", Description: "If true, report candidates without deleting (default true)"},
			"domain":    domainProp,
		},
	}, func(ctx any, args map[string]any) (string, error) {
		return gkForward(deps, "prune_stale", args)
	})

	server.RegisterTool("gk_get_health_report", mcp.ToolDef{
		GKTool:      true,
		Description: "Comprehensive health report: type/tier distribution, access patterns, temporal health, and recommendations.",
		Properties: map[string]mcp.PropDef{
			"domain": domainProp,
		},
	}, func(ctx any, args map[string]any) (string, error) {
		return gkForward(deps, "get_health_report", args)
	})

	server.RegisterTool("gk_bulk_update_confidence", mcp.ToolDef{
		GKTool:      true,
		Description: "Batch-set confidence scores for a list of entities.",
		Properties: map[string]mcp.PropDef{
			"updates": {Type: "array", Description: "Array of {name: string, confidence: number} objects"},
			"domain":  domainProp,
		},
		Required: gkRequired("updates"),
	}, func(ctx any, args map[string]any) (string, error) {
		return gkForward(deps, "bulk_update_confidence", args)
	})

}

// gkForward strips the "domain" key from args, routes to the GK pool,
// and returns the result. The domain is passed separately for pool routing.
func gkForward(deps *Dependencies, toolName string, args map[string]any) (string, error) {
	if deps.GKCallTool == nil {
		return "", fmt.Errorf("GK not configured (GK_PATH not set or GKCallTool not wired)")
	}
	domain, _ := args["domain"].(string)
	if domain == "" {
		domain = "/"
	}
	// Build args without "domain" for the GK process
	fwd := make(map[string]any, len(args))
	for k, v := range args {
		if k != "domain" {
			fwd[k] = v
		}
	}
	result, err := deps.GKCallTool(domain, toolName, fwd)
	if err != nil {
		return "", fmt.Errorf("gk.%s: %w", toolName, err)
	}
	return result, nil
}

// GKToolNames returns the list of all registered GK tool names (gk_* prefix).
// Useful for building agent tool lists.
func GKToolNames() []string {
	return []string{
		"gk_add_entities", "gk_add_relationships", "gk_add_observations",
		"gk_add_chunked_observation", "gk_update_entities", "gk_update_relationships",
		"gk_delete_entities", "gk_merge_entities",
		"gk_search_keyword", "gk_search", "gk_search_entities",
		"gk_list_entities", "gk_read_observation",
		"gk_get_entity", "gk_get_entity_profile", "gk_get_relationships",
		"gk_list_entity_types", "gk_find_paths", "gk_get_neighbors",
		"gk_extract_subgraph", "gk_get_centrality", "gk_get_timeline", "gk_validate_graph",
		"gk_get_stats", "gk_prune_stale", "gk_get_health_report", "gk_bulk_update_confidence",
	}
}

