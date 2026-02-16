package embedding

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"time"
)

// Client handles embedding generation via Ollama
type Client struct {
	baseURL        string
	model          string
	generationModel string
	client         *http.Client
}

// NewClient creates a new Ollama embedding client
func NewClient(baseURL, model string) *Client {
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}
	if model == "" {
		model = "nomic-embed-text" // good default, 768 dims
	}
	return &Client{
		baseURL:         baseURL,
		model:           model,
		generationModel: "llama3.2", // fast, available by default
		client: &http.Client{
			Timeout: 300 * time.Second, // 5 minutes for long-running compressions
		},
	}
}

// SetGenerationModel changes the model used for text generation
func (c *Client) SetGenerationModel(model string) {
	c.generationModel = model
}

// embeddingRequest is the Ollama API request format
type embeddingRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
}

// embeddingResponse is the Ollama API response format
type embeddingResponse struct {
	Embedding []float64 `json:"embedding"`
}

// Embed generates an embedding for the given text
func (c *Client) Embed(text string) ([]float64, error) {
	if text == "" {
		return nil, fmt.Errorf("empty text")
	}

	reqBody := embeddingRequest{
		Model:  c.model,
		Prompt: text,
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	resp, err := c.client.Post(
		c.baseURL+"/api/embeddings",
		"application/json",
		bytes.NewReader(jsonBody),
	)
	if err != nil {
		return nil, fmt.Errorf("ollama request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama error (status %d): %s", resp.StatusCode, string(body))
	}

	var result embeddingResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if len(result.Embedding) == 0 {
		return nil, fmt.Errorf("empty embedding returned")
	}

	return result.Embedding, nil
}

// generateRequest is the Ollama API request format for generation
type generateRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
	Stream bool   `json:"stream"`
}

// generateResponse is the Ollama API response format for generation
type generateResponse struct {
	Response string `json:"response"`
	Done     bool   `json:"done"`
}

// Generate creates text completion using Ollama
func (c *Client) Generate(prompt string) (string, error) {
	if prompt == "" {
		return "", fmt.Errorf("empty prompt")
	}

	reqBody := generateRequest{
		Model:  c.generationModel,
		Prompt: prompt,
		Stream: false,
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	start := time.Now()
	resp, err := c.client.Post(
		c.baseURL+"/api/generate",
		"application/json",
		bytes.NewReader(jsonBody),
	)
	if err != nil {
		return "", fmt.Errorf("ollama request (took %s): %w", time.Since(start), err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("ollama error (status %d, took %s): %s", resp.StatusCode, time.Since(start), string(body))
	}

	var result generateResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode response (took %s): %w", time.Since(start), err)
	}

	return result.Response, nil
}

// Summarize creates a summary of multiple text fragments
func (c *Client) Summarize(fragments []string) (string, error) {
	if len(fragments) == 0 {
		return "", fmt.Errorf("no fragments to summarize")
	}

	// Build prompt for memory summarization
	// Always summarize, even for short messages, to convert raw text to memory format
	prompt := `Convert this conversation fragment into a concise memory trace.

Guidelines:
- Capture facts, decisions, observations, insights — not just what was said
- Be concise (1-2 sentences max)
- Use past tense
- Output ONLY the memory, no commentary

Examples:
Input: "My favorite coffee shop is Blue Bottle on Market Street"
Memory: The user's favorite coffee shop is Blue Bottle on Market Street.

Input: "Sarah is my cofounder, she handles product"
Memory: Sarah is the user's cofounder who handles product.

Input: "The API returns 429 errors under load. Added exponential backoff with jitter."
Memory: The API was rate-limited; exponential backoff with jitter was added.

Input:
`
	for _, f := range fragments {
		prompt += f + "\n"
	}
	prompt += "\nMemory:"

	return c.Generate(prompt)
}

// CosineSimilarity computes similarity between two embeddings (-1 to 1)
func CosineSimilarity(a, b []float64) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}

	var dotProduct, normA, normB float64
	for i := range a {
		dotProduct += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}

	if normA == 0 || normB == 0 {
		return 0
	}

	return dotProduct / (math.Sqrt(normA) * math.Sqrt(normB))
}

// AverageEmbeddings computes the centroid of multiple embeddings
func AverageEmbeddings(embeddings [][]float64) []float64 {
	if len(embeddings) == 0 {
		return nil
	}

	dims := len(embeddings[0])
	result := make([]float64, dims)

	for _, emb := range embeddings {
		if len(emb) != dims {
			continue // skip mismatched dimensions
		}
		for i, v := range emb {
			result[i] += v
		}
	}

	n := float64(len(embeddings))
	for i := range result {
		result[i] /= n
	}

	return result
}

// UpdateCentroid updates a centroid with a new embedding using exponential moving average
func UpdateCentroid(current, new []float64, alpha float64) []float64 {
	if len(current) == 0 {
		return new
	}
	if len(new) == 0 {
		return current
	}
	if len(current) != len(new) {
		return new // dimension mismatch, use new
	}

	result := make([]float64, len(current))
	for i := range current {
		result[i] = alpha*new[i] + (1-alpha)*current[i]
	}
	return result
}
