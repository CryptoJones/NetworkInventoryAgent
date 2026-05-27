package alerts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// WebhookSink POSTs each event as JSON to a single configured URL.
// One retry on transient network/5xx failures; anything else is on the
// operator's webhook receiver to handle (idempotency via Event.Time +
// Event.IP, for example).
type WebhookSink struct {
	url        string
	authHeader string
	client     *http.Client
}

// NewWebhookSink builds a sink. Returns nil when url is empty so callers
// can wire it unconditionally from config without a guard.
//
// authHeader, when non-empty, is sent verbatim as the `Authorization`
// header on every request — operators choose `Bearer <token>` or
// `Basic <b64>` depending on their receiver.
func NewWebhookSink(url, authHeader string) *WebhookSink {
	if url == "" {
		return nil
	}
	return &WebhookSink{
		url:        url,
		authHeader: authHeader,
		client:     &http.Client{Timeout: 5 * time.Second},
	}
}

// Name implements Sink.
func (*WebhookSink) Name() string { return "webhook" }

// Emit POSTs the event. One retry on transient failure (network error or
// 5xx). 4xx is not retried because the receiver has rejected the payload
// and another identical try will not help.
func (w *WebhookSink) Emit(ctx context.Context, ev Event) error {
	body, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(500 * time.Millisecond):
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.url, bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("build request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "NetworkInventoryAgent")
		if w.authHeader != "" {
			req.Header.Set("Authorization", w.authHeader)
		}
		resp, err := w.client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		_ = resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return nil
		}
		if resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("webhook returned %d", resp.StatusCode)
			continue
		}
		return fmt.Errorf("webhook returned %d", resp.StatusCode)
	}
	return lastErr
}

// Close is a no-op — http.Client has no resources to free.
func (*WebhookSink) Close() error { return nil }
