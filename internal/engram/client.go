// Package engram provides an HTTP client for the Engram memory API.
package engram

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Client is an HTTP client for the Engram memory API.
type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

// NewClient creates a new Engram API client.
// baseURL should be like "http://localhost:8080".
// apiKey is passed as a Bearer token on all v1 requests.
func NewClient(baseURL, apiKey string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// --- Types ---

// Episode represents a raw ingested memory (Tier 1).
type Episode struct {
	ID                string    `json:"id"`
	Content           string    `json:"content"`
	TokenCount        int       `json:"token_count"`
	Source            string    `json:"source"`
	Author            string    `json:"author,omitempty"`
	AuthorID          string    `json:"author_id,omitempty"`
	Channel           string    `json:"channel,omitempty"`
	TimestampEvent    time.Time `json:"timestamp_event"`
	TimestampIngested time.Time `json:"timestamp_ingested"`
	DialogueAct       string    `json:"dialogue_act,omitempty"`
	EntropyScore      float64   `json:"entropy_score,omitempty"`
	Embedding         []float64 `json:"embedding,omitempty"`
	ReplyTo           string    `json:"reply_to,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
}

// Entity represents an extracted named entity (Tier 2).
type Entity struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Type      string    `json:"type"`
	Salience  float64   `json:"salience"`
	Aliases   []string  `json:"aliases,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Trace represents a consolidated memory (Tier 3).
// JSON field names match Engram's "engram" type.
type Trace struct {
	ID           string    `json:"id"`
	ShortID      string    `json:"short_id,omitempty"`
	Summary      string    `json:"summary"`
	Topic        string    `json:"topic,omitempty"`
	TraceType    string    `json:"engram_type,omitempty"`
	Activation   float64   `json:"activation"`
	Strength     int       `json:"strength"`
	Embedding    []float64 `json:"embedding,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	LastAccessed time.Time `json:"last_accessed"`
	LabileUntil  time.Time `json:"labile_until,omitempty"`
	SourceIDs    []string  `json:"source_ids,omitempty"`
	EntityIDs    []string  `json:"entity_ids,omitempty"`
}

// TraceContext holds a trace with its source episodes and linked entities.
// JSON field names match Engram's context response.
type TraceContext struct {
	Trace    *Trace    `json:"engram"`
	Sources  []Episode `json:"source_episodes"`
	Entities []*Entity `json:"linked_entities"`
}

// RetrievalResult holds memory retrieval results from a search.
type RetrievalResult struct {
	Traces   []*Trace    `json:"traces"`
	Episodes []*Episode  `json:"episodes"`
	Entities []*Entity   `json:"entities"`
}

// IngestEpisodeRequest is the body for POST /v1/episodes.
type IngestEpisodeRequest struct {
	Content        string    `json:"content"`
	Source         string    `json:"source"`
	Author         string    `json:"author,omitempty"`
	AuthorID       string    `json:"author_id,omitempty"`
	Channel        string    `json:"channel,omitempty"`
	TimestampEvent time.Time `json:"timestamp_event,omitempty"`
	ReplyTo        string    `json:"reply_to,omitempty"`
	Embedding      []float64 `json:"embedding,omitempty"`
}

// IngestResult is the response from episode/thought ingestion.
type IngestResult struct {
	ID string `json:"id"`
}

// ConsolidateResult is the response from POST /v1/consolidate.
type ConsolidateResult struct {
	TracesCreated int   `json:"engrams_created"`
	DurationMS    int64 `json:"duration_ms"`
}

// DecayResult is the response from POST /v1/activation/decay.
type DecayResult struct {
	Updated int `json:"updated"`
}

// --- Health ---

// Health checks the server. Returns nil if healthy.
func (c *Client) Health() error {
	resp, err := c.httpClient.Get(c.baseURL + "/health")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health check failed: %s", resp.Status)
	}
	return nil
}

// --- Ingest ---

// IngestEpisode stores a new episode and returns its ID.
func (c *Client) IngestEpisode(req IngestEpisodeRequest) (*IngestResult, error) {
	var result IngestResult
	if err := c.post("/v1/episodes", req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// IngestThought stores a free-form thought and returns its ID.
func (c *Client) IngestThought(content string) (*IngestResult, error) {
	body := map[string]string{"content": content}
	var result IngestResult
	if err := c.post("/v1/thoughts", body, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// --- Consolidation ---

// Consolidate runs the consolidation pipeline.
func (c *Client) Consolidate() (*ConsolidateResult, error) {
	var result ConsolidateResult
	if err := c.post("/v1/consolidate", nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// --- Search ---

// Search retrieves relevant memories for the given query via Engram's semantic search.
// limit <= 0 uses the server default (10).
// Returns a RetrievalResult with Traces populated; Episodes and Entities are empty.
func (c *Client) Search(query string, limit int) (*RetrievalResult, error) {
	body := map[string]any{
		"query":  query,
		"detail": "full",
	}
	if limit > 0 {
		body["limit"] = limit
	}
	var traces []*Trace
	if err := c.post("/v1/engrams/search", body, &traces); err != nil {
		return nil, err
	}
	return &RetrievalResult{Traces: traces}, nil
}

// --- Traces ---

// ListTraces returns all consolidated memory traces.
func (c *Client) ListTraces() ([]*Trace, error) {
	params := url.Values{}
	params.Set("detail", "full")
	var traces []*Trace
	if err := c.get("/v1/engrams", params, &traces); err != nil {
		return nil, err
	}
	return traces, nil
}

// GetTrace fetches a trace by full ID or 5-char prefix.
// level: 0=uncompressed, 4/8/16/32=word-count target for summary.
func (c *Client) GetTrace(id string, level int) (*Trace, error) {
	params := url.Values{}
	params.Set("detail", "full")
	if level >= 0 {
		params.Set("level", strconv.Itoa(level))
	}
	var trace Trace
	if err := c.get("/v1/engrams/"+url.PathEscape(id), params, &trace); err != nil {
		return nil, err
	}
	return &trace, nil
}

// GetTraceContext fetches a trace with its source episodes and linked entities.
func (c *Client) GetTraceContext(id string) (*TraceContext, error) {
	params := url.Values{}
	params.Set("detail", "full")
	var ctx TraceContext
	if err := c.get("/v1/engrams/"+url.PathEscape(id)+"/context", params, &ctx); err != nil {
		return nil, err
	}
	return &ctx, nil
}

// DeleteTrace deletes a trace by ID.
func (c *Client) DeleteTrace(id string) error {
	req, err := http.NewRequest(http.MethodDelete, c.baseURL+"/v1/engrams/"+url.PathEscape(id), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	return c.parseError(resp)
}

// ReinforceTrace boosts a trace's activation. alpha=0 uses the server default (0.3).
func (c *Client) ReinforceTrace(id string, embedding []float64, alpha float64) error {
	body := map[string]any{}
	if len(embedding) > 0 {
		body["embedding"] = embedding
	}
	if alpha > 0 {
		body["alpha"] = alpha
	}
	return c.post("/v1/engrams/"+url.PathEscape(id)+"/reinforce", body, nil)
}

// GetActivatedTraces returns traces with activation above threshold.
// threshold=0 returns all traces sorted by activation.
func (c *Client) GetActivatedTraces(threshold float64, limit int) ([]*Trace, error) {
	params := url.Values{}
	params.Set("detail", "full")
	params.Set("threshold", strconv.FormatFloat(threshold, 'f', -1, 64))
	if limit > 0 {
		params.Set("limit", strconv.Itoa(limit))
	}
	var traces []*Trace
	if err := c.get("/v1/engrams", params, &traces); err != nil {
		return nil, err
	}
	return traces, nil
}

// BoostTraces boosts activation for a set of traces.
// boost=0 uses the server default (0.1).
func (c *Client) BoostTraces(traceIDs []string, boost, threshold float64) error {
	body := map[string]any{
		"ids": traceIDs,
	}
	if boost > 0 {
		body["boost"] = boost
	}
	if threshold > 0 {
		body["threshold"] = threshold
	}
	return c.post("/v1/engrams/boost", body, nil)
}

// --- Episodes ---

// GetEpisode fetches an episode by full ID or 5-char prefix.
func (c *Client) GetEpisode(id string) (*Episode, error) {
	var ep Episode
	if err := c.get("/v1/episodes/"+url.PathEscape(id), nil, &ep); err != nil {
		return nil, err
	}
	return &ep, nil
}

// GetRecentEpisodes returns the most recent episodes for a channel.
// channel="" returns episodes across all channels.
func (c *Client) GetRecentEpisodes(channel string, limit int) ([]*Episode, error) {
	params := url.Values{}
	if channel != "" {
		params.Set("channel", channel)
	}
	if limit > 0 {
		params.Set("limit", strconv.Itoa(limit))
	}
	var episodes []*Episode
	if err := c.get("/v1/episodes", params, &episodes); err != nil {
		return nil, err
	}
	return episodes, nil
}

// GetUnconsolidatedEpisodeIDs returns the set of unconsolidated episode IDs for a channel.
// Engram returns a JSON array of episode objects; we extract just the IDs.
func (c *Client) GetUnconsolidatedEpisodeIDs(channel string) (map[string]bool, error) {
	params := url.Values{}
	params.Set("unconsolidated", "true")
	if channel != "" {
		params.Set("channel", channel)
	}
	var episodes []*Episode
	if err := c.get("/v1/episodes", params, &episodes); err != nil {
		return nil, err
	}
	result := make(map[string]bool, len(episodes))
	for _, ep := range episodes {
		result[ep.ID] = true
	}
	return result, nil
}

// GetUnconsolidatedEpisodeCount returns the number of episodes not yet linked to any trace.
func (c *Client) GetUnconsolidatedEpisodeCount() (int, error) {
	params := url.Values{}
	params.Set("unconsolidated", "true")
	var result struct {
		Count int `json:"count"`
	}
	if err := c.get("/v1/episodes/count", params, &result); err != nil {
		return 0, err
	}
	return result.Count, nil
}

// AddEpisodeEdge creates a directed edge between two episodes (e.g. FOLLOWS).
// edgeType should be "follows" or another semantic label.
// confidence should be 0.0–1.0; 0 is treated as 1.0 by the server.
func (c *Client) AddEpisodeEdge(fromID, toID, edgeType string, confidence float64) error {
	body := map[string]any{
		"target_id": toID,
		"edge_type": edgeType,
	}
	if confidence > 0 {
		body["confidence"] = confidence
	}
	return c.post("/v1/episodes/"+url.PathEscape(fromID)+"/edges", body, nil)
}

// GetEpisodeSummariesBatch fetches summaries for multiple episodes at a compression level.
// level: 0=uncompressed, 4/8/16/32=word-count target (standard levels: 4, 8, 16, 32).
// Returns a map of episodeID → summary string.
func (c *Client) GetEpisodeSummariesBatch(episodeIDs []string, level int) (map[string]string, error) {
	body := map[string]any{
		"episode_ids": episodeIDs,
		"level":       level,
	}
	var result map[string]string
	if err := c.post("/v1/episodes/summaries", body, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// --- Entities ---

// ListEntities returns entities. entityType="" returns all; limit<=0 uses server default (100).
func (c *Client) ListEntities(entityType string, limit int) ([]*Entity, error) {
	params := url.Values{}
	if entityType != "" {
		params.Set("type", entityType)
	}
	if limit > 0 {
		params.Set("limit", strconv.Itoa(limit))
	}
	var entities []*Entity
	if err := c.get("/v1/entities", params, &entities); err != nil {
		return nil, err
	}
	return entities, nil
}

// --- Activation ---

// DecayActivation applies exponential decay to trace activations.
// Pass zero values to use server defaults.
func (c *Client) DecayActivation(lambda, floor float64) (*DecayResult, error) {
	body := map[string]any{}
	if lambda > 0 {
		body["lambda"] = lambda
	}
	if floor > 0 {
		body["floor"] = floor
	}
	var result DecayResult
	if err := c.post("/v1/activation/decay", body, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// --- Management ---

// Flush triggers consolidation and cleanup.
func (c *Client) Flush() error {
	return c.post("/v1/memory/flush", nil, nil)
}

// Reset clears all memory. Destructive and irreversible.
func (c *Client) Reset() error {
	req, err := http.NewRequest(http.MethodDelete, c.baseURL+"/v1/memory/reset", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return c.parseError(resp)
	}
	return nil
}

// --- HTTP helpers ---

func (c *Client) get(path string, params url.Values, out any) error {
	u := c.baseURL + path
	if len(params) > 0 {
		u += "?" + params.Encode()
	}
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return c.parseError(resp)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

func (c *Client) post(path string, body any, out any) error {
	var r io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(data)
	}

	req, err := http.NewRequest(http.MethodPost, c.baseURL+path, r)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return c.parseError(resp)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

type apiError struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

func (c *Client) parseError(resp *http.Response) error {
	body, _ := io.ReadAll(resp.Body)
	var apiErr apiError
	if json.Unmarshal(body, &apiErr) == nil && apiErr.Code != "" {
		return fmt.Errorf("engram API error [%s] %s: %s", resp.Status, apiErr.Code, apiErr.Error)
	}
	return fmt.Errorf("engram API error [%s]: %s", resp.Status, string(body))
}
