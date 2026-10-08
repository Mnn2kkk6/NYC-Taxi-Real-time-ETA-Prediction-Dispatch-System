// Package mlclient talks to the Python FastAPI prediction service over HTTP.
//
// Design points that matter for the interview story:
//   - ONE shared *http.Client with connection pooling — never created per request.
//   - Every attempt has a hard per-attempt timeout via context.WithTimeout.
//   - On failure/timeout, retries with exponential backoff (100ms, 200ms, 400ms...),
//     capped at MaxRetries — never retries forever.
//   - The caller's context is respected throughout: if the caller cancels
//     (e.g. client disconnected), retries stop immediately instead of
//     burning a worker goroutine on a request nobody wants anymore.
package mlclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"nyctaxi/backend/internal/models"
)

type Client struct {
	baseURL    string
	httpClient *http.Client
	maxRetries int
	timeout    time.Duration
}

func New(baseURL string, timeout time.Duration, maxRetries int) *Client {
	return &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			// Timeout here is a hard ceiling on the whole request (incl. body read);
			// the per-attempt context deadline below is what actually drives retries.
			Timeout: timeout + 500*time.Millisecond,
			Transport: &http.Transport{
				MaxIdleConns:        50,
				MaxIdleConnsPerHost: 50,
				IdleConnTimeout:     90 * time.Second,
			},
		},
		maxRetries: maxRetries,
		timeout:    timeout,
	}
}

// ErrMLServiceUnavailable is returned when every retry attempt failed.
type ErrMLServiceUnavailable struct {
	Attempts int
	LastErr  error
}

func (e *ErrMLServiceUnavailable) Error() string {
	return fmt.Sprintf("ml service unavailable after %d attempts: %v", e.Attempts, e.LastErr)
}

// Predict calls POST /predict on the Python service, retrying on failure
// with exponential backoff. ctx is the caller's context (e.g. derived from
// the incoming HTTP request or a worker job) — if it's cancelled, we stop
// retrying immediately rather than sleeping through a dead request.
func (c *Client) Predict(ctx context.Context, req models.PredictionRequest) (*models.PredictionResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	var lastErr error
	backoff := 100 * time.Millisecond

	for attempt := 1; attempt <= c.maxRetries; attempt++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		resp, err := c.doOnce(ctx, body)
		if err == nil {
			return resp, nil
		}
		lastErr = err

		if attempt < c.maxRetries {
			timer := time.NewTimer(backoff)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
			backoff *= 2
		}
	}

	return nil, &ErrMLServiceUnavailable{Attempts: c.maxRetries, LastErr: lastErr}
}

func (c *Client) doOnce(ctx context.Context, body []byte) (*models.PredictionResponse, error) {
	attemptCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(attemptCtx, http.MethodPost, c.baseURL+"/predict", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ml service returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var predResp models.PredictionResponse
	if err := json.Unmarshal(respBody, &predResp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}
	return &predResp, nil
}

// HealthCheck pings the Python service's /health endpoint. Used by the Go
// server's own /health handler to report ml_service_up.
func (c *Client) HealthCheck(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, 1*time.Second)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/health", nil)
	if err != nil {
		return false
	}
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}
