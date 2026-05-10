package health

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// maxStatusBytes caps the /status response body to prevent memory exhaustion
// if a misbehaving or malicious peer sends a large payload (OWASP A10).
const maxStatusBytes = 1 << 20 // 1 MiB

// Client fetches health and status from a peer agent's HTTP server.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

func NewClient(baseURL string) *Client {
	return &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

// Ping returns nil if the peer is alive and reporting healthy.
func (c *Client) Ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/health", nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("health check failed: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("peer returned status %d", resp.StatusCode)
	}
	return nil
}

// FetchStatus retrieves the full Status from the peer.
func (c *Client) FetchStatus(ctx context.Context) (Status, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/status", nil)
	if err != nil {
		return Status{}, fmt.Errorf("build request: %w", err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return Status{}, fmt.Errorf("fetch status failed: %w", err)
	}
	defer resp.Body.Close()

	var s Status
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxStatusBytes)).Decode(&s); err != nil {
		return Status{}, fmt.Errorf("decode status: %w", err)
	}
	return s, nil
}
