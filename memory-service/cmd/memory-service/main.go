// memory-service is a standalone HTTP API for the Bud memory system.
//
// It exposes endpoints for ingesting episodes, recalling memories via
// spreading activation, and assembling compressed context windows.
//
// External dependencies:
//   - SQLite (embedded, via go-sqlite3)
//   - Ollama (for embeddings and text generation)
//   - spaCy NER sidecar (optional, for entity pre-filtering)
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/vthunder/bud2/memory-service/pkg/embedding"
	"github.com/vthunder/bud2/memory-service/pkg/extract"
	"github.com/vthunder/bud2/memory-service/pkg/filter"
	"github.com/vthunder/bud2/memory-service/pkg/graph"
	"github.com/vthunder/bud2/memory-service/pkg/ner"
)

// Config holds service configuration, populated from environment variables.
type Config struct {
	Port         string // HTTP port (default "8230")
	DataDir      string // Directory for SQLite database (default "./data")
	OllamaURL    string // Ollama API base URL (default "http://localhost:11434")
	EmbedModel   string // Ollama embedding model (default "nomic-embed-text")
	GenModel     string // Ollama generation model (default "llama3.2")
	NERSidecar   string // spaCy NER sidecar URL (default "http://localhost:5100")
}

func loadConfig() Config {
	cfg := Config{
		Port:       envOr("MEMORY_PORT", "8230"),
		DataDir:    envOr("MEMORY_DATA_DIR", "./data"),
		OllamaURL:  envOr("OLLAMA_URL", "http://localhost:11434"),
		EmbedModel: envOr("OLLAMA_EMBED_MODEL", "nomic-embed-text"),
		GenModel:   envOr("OLLAMA_GEN_MODEL", "llama3.2"),
		NERSidecar: envOr("NER_SIDECAR_URL", "http://localhost:5100"),
	}
	return cfg
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// Service holds all the initialized components.
type Service struct {
	db        *graph.DB
	embedder  *embedding.Client
	nerClient *ner.Client
	fastNER   *extract.FastExtractor
	deepNER   *extract.DeepExtractor
	resolver  *extract.Resolver
	filter    *filter.EntropyFilter
	cfg       Config
}

func main() {
	cfg := loadConfig()

	// Ensure data directory exists
	if err := os.MkdirAll(cfg.DataDir, 0755); err != nil {
		log.Fatalf("Failed to create data directory: %v", err)
	}

	// Open graph database
	db, err := graph.Open(cfg.DataDir)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	// Initialize embedding client
	embedClient := embedding.NewClient(cfg.OllamaURL, cfg.EmbedModel)
	embedClient.SetGenerationModel(cfg.GenModel)

	// Initialize NER client
	nerClient := ner.NewClient(cfg.NERSidecar)

	// Initialize extractors
	fastNER := extract.NewFastExtractor()
	deepNER := extract.NewDeepExtractor(embedClient)
	resolver := extract.NewResolver(db, embedClient)

	// Initialize filter
	entropyFilter := filter.NewEntropyFilter(embedClient)

	svc := &Service{
		db:        db,
		embedder:  embedClient,
		nerClient: nerClient,
		fastNER:   fastNER,
		deepNER:   deepNER,
		resolver:  resolver,
		filter:    entropyFilter,
		cfg:       cfg,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", svc.handleHealth)
	mux.HandleFunc("POST /ingest", svc.handleIngest)
	mux.HandleFunc("POST /recall", svc.handleRecall)
	mux.HandleFunc("POST /context", svc.handleContext)

	server := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: mux,
	}

	// Graceful shutdown
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		log.Println("Shutting down...")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		server.Shutdown(ctx)
	}()

	log.Printf("memory-service listening on :%s (data: %s)", cfg.Port, cfg.DataDir)
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatalf("Server error: %v", err)
	}
}

// ─── Health ──────────────────────────────────────────────────────────────────

func (s *Service) handleHealth(w http.ResponseWriter, r *http.Request) {
	stats, err := s.db.Stats()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"status": "error", "error": err.Error()})
		return
	}

	health := map[string]any{
		"status":       "ok",
		"ner_sidecar":  s.nerClient.Healthy(),
		"graph_stats":  stats,
	}
	writeJSON(w, http.StatusOK, health)
}

// ─── Ingest ──────────────────────────────────────────────────────────────────

// IngestRequest is the request body for POST /ingest.
type IngestRequest struct {
	Content   string `json:"content"`             // Message text (required)
	Source    string `json:"source"`              // e.g. "discord", "slack", "api"
	Author    string `json:"author,omitempty"`
	AuthorID  string `json:"author_id,omitempty"`
	Channel   string `json:"channel,omitempty"`
	ReplyTo   string `json:"reply_to,omitempty"`  // Episode ID this replies to
	Timestamp string `json:"timestamp,omitempty"` // RFC3339; defaults to now
}

// IngestResponse is the response for POST /ingest.
type IngestResponse struct {
	EpisodeID string           `json:"episode_id"`
	ShortID   string           `json:"short_id"`
	Entities  []EntityResult   `json:"entities,omitempty"`
	Entropy   float64          `json:"entropy_score"`
}

// EntityResult is a resolved entity returned from ingestion.
type EntityResult struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	IsNew     bool   `json:"is_new"`
	MatchedBy string `json:"matched_by"`
}

func (s *Service) handleIngest(w http.ResponseWriter, r *http.Request) {
	var req IngestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	if req.Content == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "content is required"})
		return
	}
	if req.Source == "" {
		req.Source = "api"
	}

	ts := time.Now()
	if req.Timestamp != "" {
		if parsed, err := time.Parse(time.RFC3339, req.Timestamp); err == nil {
			ts = parsed
		}
	}

	// Classify dialogue act
	dialogueAct := string(filter.ClassifyDialogueAct(req.Content))

	// Score entropy
	entropyResult, _ := s.filter.Score(req.Content)
	entropyScore := 0.5
	var emb []float64
	if entropyResult != nil {
		entropyScore = entropyResult.Score
		emb = entropyResult.Embedding
	}

	// Generate embedding if filter didn't
	if len(emb) == 0 {
		emb, _ = s.embedder.Embed(req.Content)
	}

	// Build episode ID
	episodeID := fmt.Sprintf("ep-%d-%s", ts.UnixNano(), hashPrefix(req.Content))

	ep := &graph.Episode{
		ID:              episodeID,
		Content:         req.Content,
		Source:          req.Source,
		Author:          req.Author,
		AuthorID:        req.AuthorID,
		Channel:         req.Channel,
		TimestampEvent:  ts,
		DialogueAct:     dialogueAct,
		EntropyScore:    entropyScore,
		Embedding:       emb,
		ReplyTo:         req.ReplyTo,
	}

	if err := s.db.AddEpisode(ep); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to store episode: " + err.Error()})
		return
	}

	// Generate pyramid summaries (async)
	s.db.GenerateEpisodeSummaries(*ep, s.embedder)

	// Entity extraction: NER pre-filter → deep extraction → resolution
	var entityResults []EntityResult
	shouldExtract := true

	// Use NER sidecar as pre-filter if available
	if s.nerClient.Healthy() {
		nerResp, err := s.nerClient.Extract(req.Content)
		if err == nil && !nerResp.HasEntities {
			shouldExtract = false // No entities detected by spaCy
		}
	}

	if shouldExtract {
		result, err := s.deepNER.ExtractAll(req.Content)
		if err == nil && result != nil {
			config := extract.DefaultResolveConfig()

			for _, ent := range result.Entities {
				resolved, err := s.resolver.Resolve(ent, config)
				if err != nil || resolved == nil {
					continue
				}

				// Link episode to entity
				s.db.LinkEpisodeToEntity(ep.ID, resolved.Entity.ID)

				entityResults = append(entityResults, EntityResult{
					ID:        resolved.Entity.ID,
					Name:      resolved.Entity.Name,
					Type:      string(resolved.Entity.Type),
					IsNew:     resolved.IsNew,
					MatchedBy: resolved.MatchedBy,
				})
			}

			// Process relationships
			for _, rel := range result.Relationships {
				edgeType := extract.PredicateToEdgeType(rel.Predicate)
				// Find subject and object entity IDs
				subjectEntity := findEntityByName(entityResults, rel.Subject)
				objectEntity := findEntityByName(entityResults, rel.Object)
				if subjectEntity != "" && objectEntity != "" {
					s.db.AddEntityRelation(subjectEntity, objectEntity, edgeType, rel.Confidence)
				}
			}
		}
	}

	writeJSON(w, http.StatusOK, IngestResponse{
		EpisodeID: ep.ID,
		ShortID:   ep.ShortID,
		Entities:  entityResults,
		Entropy:   entropyScore,
	})
}

// ─── Recall ──────────────────────────────────────────────────────────────────

// RecallRequest is the request body for POST /recall.
type RecallRequest struct {
	Query        string   `json:"query"`                   // Natural language query (required)
	Limit        int      `json:"limit,omitempty"`         // Max traces to return (default 10)
	ContextIDs   []string `json:"context_ids,omitempty"`   // Currently active trace IDs for context-biased retrieval
}

// RecallResponse is the response for POST /recall.
type RecallResponse struct {
	Traces []RecalledTrace `json:"traces"`
}

// RecalledTrace is a memory trace with activation score.
type RecalledTrace struct {
	ID         string  `json:"id"`
	ShortID    string  `json:"short_id"`
	Summary    string  `json:"summary"`
	Topic      string  `json:"topic,omitempty"`
	TraceType  string  `json:"trace_type"`
	Activation float64 `json:"activation"`
	Strength   int     `json:"strength"`
	CreatedAt  string  `json:"created_at"`
}

func (s *Service) handleRecall(w http.ResponseWriter, r *http.Request) {
	var req RecallRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	if req.Query == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "query is required"})
		return
	}
	if req.Limit <= 0 {
		req.Limit = 10
	}

	// Generate query embedding
	queryEmb, err := s.embedder.Embed(req.Query)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "embedding failed: " + err.Error()})
		return
	}

	// Retrieve using spreading activation
	var result *graph.RetrievalResult
	if len(req.ContextIDs) > 0 {
		result, err = s.db.RetrieveWithContext(queryEmb, req.Query, req.ContextIDs, req.Limit)
	} else {
		result, err = s.db.Retrieve(queryEmb, req.Query, req.Limit)
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "retrieval failed: " + err.Error()})
		return
	}

	// Boost accessed traces
	var accessedIDs []string
	var traces []RecalledTrace
	for _, tr := range result.Traces {
		accessedIDs = append(accessedIDs, tr.ID)
		traces = append(traces, RecalledTrace{
			ID:         tr.ID,
			ShortID:    tr.ShortID,
			Summary:    tr.Summary,
			Topic:      tr.Topic,
			TraceType:  string(tr.TraceType),
			Activation: tr.Activation,
			Strength:   tr.Strength,
			CreatedAt:  tr.CreatedAt.Format(time.RFC3339),
		})
	}
	if len(accessedIDs) > 0 {
		s.db.BoostTraceAccess(accessedIDs, 0.1)
	}

	writeJSON(w, http.StatusOK, RecallResponse{Traces: traces})
}

// ─── Context ─────────────────────────────────────────────────────────────────

// ContextRequest is the request body for POST /context.
type ContextRequest struct {
	Channel    string `json:"channel,omitempty"`    // Filter episodes by channel
	Query      string `json:"query,omitempty"`      // Optional query to bias retrieved memories
	MaxEpisodes int   `json:"max_episodes,omitempty"` // Default 30
	MaxTraces   int   `json:"max_traces,omitempty"`   // Default 10
}

// ContextResponse is the response for POST /context.
type ContextResponse struct {
	RecentConversation string          `json:"recent_conversation"` // Pyramid-compressed recent episodes
	RetrievedMemories  []RecalledTrace `json:"retrieved_memories,omitempty"`
	Stats              ContextStats    `json:"stats"`
}

// ContextStats provides context assembly statistics.
type ContextStats struct {
	EpisodeCount int `json:"episode_count"`
	TraceCount   int `json:"trace_count"`
}

func (s *Service) handleContext(w http.ResponseWriter, r *http.Request) {
	var req ContextRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	if req.MaxEpisodes <= 0 {
		req.MaxEpisodes = 30
	}
	if req.MaxTraces <= 0 {
		req.MaxTraces = 10
	}

	// Fetch recent episodes
	episodes, err := s.db.GetRecentEpisodes(req.Channel, req.MaxEpisodes)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to get episodes: " + err.Error()})
		return
	}

	// Build pyramid-compressed conversation context
	// Last 5: full content, next 10: L32, next 15: L8
	var lines []string
	for i, ep := range episodes {
		var content string
		if i < 5 {
			// Full content
			content = ep.Content
		} else if i < 15 {
			// L32 summary
			summary, _ := s.db.GetEpisodeSummary(ep.ID, graph.CompressionLevel32)
			if summary != nil {
				content = summary.Summary
			} else {
				content = ep.Content
			}
		} else {
			// L8 summary
			summary, _ := s.db.GetEpisodeSummary(ep.ID, graph.CompressionLevel8)
			if summary != nil {
				content = summary.Summary
			} else {
				content = ep.Content
			}
		}

		ts := ep.TimestampEvent.Format("15:04")
		author := ep.Author
		if author == "" {
			author = ep.Source
		}
		lines = append(lines, fmt.Sprintf("[%s] [%s] %s: %s", ep.ShortID, ts, author, content))
	}

	// Reverse so oldest is first
	for i, j := 0, len(lines)-1; i < j; i, j = i+1, j-1 {
		lines[i], lines[j] = lines[j], lines[i]
	}

	resp := ContextResponse{
		RecentConversation: strings.Join(lines, "\n"),
		Stats: ContextStats{
			EpisodeCount: len(episodes),
		},
	}

	// Optionally retrieve relevant memories
	if req.Query != "" {
		queryEmb, err := s.embedder.Embed(req.Query)
		if err == nil {
			result, err := s.db.Retrieve(queryEmb, req.Query, req.MaxTraces)
			if err == nil {
				for _, tr := range result.Traces {
					resp.RetrievedMemories = append(resp.RetrievedMemories, RecalledTrace{
						ID:         tr.ID,
						ShortID:    tr.ShortID,
						Summary:    tr.Summary,
						Topic:      tr.Topic,
						TraceType:  string(tr.TraceType),
						Activation: tr.Activation,
						Strength:   tr.Strength,
						CreatedAt:  tr.CreatedAt.Format(time.RFC3339),
					})
				}
				resp.Stats.TraceCount = len(resp.RetrievedMemories)
			}
		}
	}

	writeJSON(w, http.StatusOK, resp)
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func hashPrefix(s string) string {
	h := fmt.Sprintf("%x", []byte(s))
	if len(h) > 8 {
		return h[:8]
	}
	return h
}

func findEntityByName(entities []EntityResult, name string) string {
	nameLower := strings.ToLower(name)
	for _, e := range entities {
		if strings.ToLower(e.Name) == nameLower {
			return e.ID
		}
	}
	return ""
}
