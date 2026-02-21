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
		baseURL: baseURL,
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
	ShortID           string    `json:"short_id"`
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
type Trace struct {
	ID           string    `json:"id"`
	ShortID      string    `json:"short_id"`
	Summary      string    `json:"summary"`
	Topic        string    `json:"topic,omitempty"`
	TraceType    string    `json:"trace_type,omitempty"`
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
type TraceContext struct {
	Trace    *Trace    `json:"trace"`
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
	ID      string `json:"id"`
	ShortID string `json:"short_id,omitempty"`
}

// ConsolidateResult is the response from POST /v1/consolidate.
type ConsolidateResult struct {
	TracesCreated int   `json:"traces_created"`
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

// Search retrieves relevant memories for the given query.
// limit <= 0 uses the server default (10).
func (c *Client) Search(query string, limit int) (*RetrievalResult, error) {
	body := map[string]any{"query": query}
	if limit > 0 {
		body["limit"] = limit
	}
	var result RetrievalResult
	if err := c.post("/v1/search", body, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// --- Traces ---

// ListTraces returns all consolidated memory traces.
func (c *Client) ListTraces() ([]*Trace, error) {
	var traces []*Trace
	if err := c.get("/v1/traces", nil, &traces); err != nil {
		return nil, err
	}
	return traces, nil
}

// GetTrace fetches a trace by full ID or short ID.
// level: 0=raw, 1=L1 summary (default), 2=L2 summary.
func (c *Client) GetTrace(id string, level int) (*Trace, error) {
	params := url.Values{}
	if level >= 0 {
		params.Set("level", strconv.Itoa(level))
	}
	var trace Trace
	if err := c.get("/v1/traces/"+url.PathEscape(id), params, &trace); err != nil {
		return nil, err
	}
	return &trace, nil
}

// GetTraceContext fetches a trace with its source episodes and linked entities.
func (c *Client) GetTraceContext(id string) (*TraceContext, error) {
	var ctx TraceContext
	if err := c.get("/v1/traces/"+url.PathEscape(id)+"/context", nil, &ctx); err != nil {
		return nil, err
	}
	return &ctx, nil
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
	return c.post("/v1/traces/"+url.PathEscape(id)+"/reinforce", body, nil)
}

// --- Episodes ---

// GetEpisode fetches an episode by full ID or short ID.
func (c *Client) GetEpisode(id string) (*Episode, error) {
	var ep Episode
	if err := c.get("/v1/episodes/"+url.PathEscape(id), nil, &ep); err != nil {
		return nil, err
	}
	return &ep, nil
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
