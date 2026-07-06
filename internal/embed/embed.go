// Package embed is an optional client for an OpenAI-compatible embeddings
// endpoint (POST /v1/embeddings). It lets the semantic cache turn raw prompt
// text into vectors. It is entirely optional: when unconfigured, the core
// server makes no network calls and the vector/semantic-cache commands still
// work with client-supplied vectors. The same endpoint shape is served by
// OpenAI and by a local Ollama instance, so the demo works with no paid API.
package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// ErrNotConfigured is returned by Embed when no endpoint URL was provided.
var ErrNotConfigured = errors.New("embeddings provider not configured: set CACHEPOT_EMBED_URL (or pass a VECTOR to the command)")

// Client calls an OpenAI-compatible embeddings endpoint.
type Client struct {
	url    string
	model  string
	apiKey string
	http   *http.Client
}

// Config holds the provider settings, normally sourced from CACHEPOT_EMBED_* env
// vars.
type Config struct {
	URL    string // e.g. https://api.openai.com/v1/embeddings or http://localhost:11434/v1/embeddings
	Model  string // e.g. text-embedding-3-small or nomic-embed-text
	APIKey string // optional bearer token (not needed for local Ollama)
}

// New returns a client. If cfg.URL is empty, the client is unconfigured and
// Embed returns ErrNotConfigured.
func New(cfg Config) *Client {
	return &Client{
		url:    cfg.URL,
		model:  cfg.Model,
		apiKey: cfg.APIKey,
		http:   &http.Client{Timeout: 30 * time.Second},
	}
}

// Configured reports whether an endpoint URL was supplied.
func (c *Client) Configured() bool { return c.url != "" }

type embedRequest struct {
	Model string `json:"model"`
	Input string `json:"input"`
}

type embedResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Embed returns the embedding vector for text.
func (c *Client) Embed(ctx context.Context, text string) ([]float32, error) {
	if !c.Configured() {
		return nil, ErrNotConfigured
	}
	body, err := json.Marshal(embedRequest{Model: c.model, Input: text})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var out embedResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode embeddings response: %w", err)
	}
	if out.Error != nil {
		return nil, fmt.Errorf("embeddings provider error: %s", out.Error.Message)
	}
	if len(out.Data) == 0 || len(out.Data[0].Embedding) == 0 {
		return nil, fmt.Errorf("embeddings provider returned no vector (status %d)", resp.StatusCode)
	}
	return out.Data[0].Embedding, nil
}
