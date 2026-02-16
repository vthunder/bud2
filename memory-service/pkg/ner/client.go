// Package ner provides a client for the spaCy NER sidecar service.
package ner

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Entity represents a named entity found by the NER sidecar.
type Entity struct {
	Text  string `json:"text"`
	Label string `json:"label"`
	Start int    `json:"start"`
	End   int    `json:"end"`
}

// ExtractResponse is the response from the NER sidecar /extract endpoint.
type ExtractResponse struct {
	Entities    []Entity `json:"entities"`
	HasEntities bool     `json:"has_entities"`
	DurationMs  float64  `json:"duration_ms"`
}

// Client communicates with the spaCy NER sidecar.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// NewClient creates a new NER sidecar client.
func NewClient(baseURL string) *Client {
	return &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

// Extract sends text to the NER sidecar and returns extracted entities.
func (c *Client) Extract(text string) (*ExtractResponse, error) {
	body, err := json.Marshal(map[string]string{"text": text})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	resp, err := c.httpClient.Post(c.baseURL+"/extract", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	var result ExtractResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return &result, nil
}

// Healthy checks if the NER sidecar is responding.
func (c *Client) Healthy() bool {
	resp, err := c.httpClient.Get(c.baseURL + "/health")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}
