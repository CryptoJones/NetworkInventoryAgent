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
	token      string
	httpClient *http.Client
}

// ClientOptions configures Client construction. Both fields are optional —
// zero values produce the simple unauthenticated loopback client.
type ClientOptions struct {
	// Token is the bearer token sent on every request. Empty disables auth.
	Token string
	// HTTPClient overrides the default 5 s plain HTTP client. Production
	// callers pass a tracing-instrumented and/or TLS-configured client.
	HTTPClient *http.Client
}

// NewClient builds an unauthenticated client (the simplest case).
// Use NewClientWith for off-loopback peers and/or custom transport.
func NewClient(baseURL string) *Client {
	return NewClientWith(baseURL, ClientOptions{})
}

// NewAuthedClient builds a client that sends `Authorization: Bearer <token>`.
// Equivalent to NewClientWith(baseURL, ClientOptions{Token: token}); preserved
// for call sites that don't need a custom transport.
func NewAuthedClient(baseURL, token string) *Client {
	return NewClientWith(baseURL, ClientOptions{Token: token})
}

// NewClientWith is the full-control constructor. Use it when you need a
// custom transport for tracing, TLS pinning, or both.
func NewClientWith(baseURL string, opts ClientOptions) *Client {
	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 5 * time.Second}
	}
	return &Client{
		baseURL:    baseURL,
		token:      opts.Token,
		httpClient: httpClient,
	}
}

func (c *Client) authorize(req *http.Request) {
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
}

// Ping returns nil if the peer is alive and reporting healthy.
func (c *Client) Ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/health", nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	c.authorize(req)
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
	c.authorize(req)
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
