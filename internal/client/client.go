// Package client is a small typed HTTP client for the Inngest REST API.
//
// It covers the endpoints the Terraform provider needs: environments, event
// keys, signing keys and apps from the v2 API, webhooks from the v2 API
// (create and list) and the v1 API (get, update and delete).
//
// The v2 API takes an API key as a Bearer token and selects the environment
// with the X-Inngest-Env header. The v1 API takes the environment's signing
// key instead, so the client looks that key up through the v2 API when it
// needs it.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/terraform-plugin-log/tflog"
)

// DefaultBaseURL is the Inngest Cloud API. The version prefix (/v1 or /v2) is
// added per request.
const DefaultBaseURL = "https://api.inngest.com"

// EnvHeader selects the environment of a request.
const EnvHeader = "X-Inngest-Env"

// maxErrorBody caps how much of an error body ends up in a message.
const maxErrorBody = 1024

// Client talks to the Inngest REST API.
type Client struct {
	APIKey     string
	BaseURL    string
	HTTPClient *http.Client
	UserAgent  string

	// KeyError, when set, is returned by every call instead of sending the
	// request. The provider sets it when it has no usable API key, so a
	// configuration that never calls the API still plans without one.
	KeyError error

	// MaxRetries is how many times a request is retried after a 429 or, for
	// requests that are safe to repeat, a 5xx or a network error.
	MaxRetries int
	// MinBackoff and MaxBackoff bound the wait between retries. A Retry-After
	// header wins when it is shorter than MaxBackoff.
	MinBackoff, MaxBackoff time.Duration
	// Sleep waits between retries. Tests replace it.
	Sleep func(ctx context.Context, d time.Duration) error

	mu sync.Mutex
	// signingKeys caches, per environment, the signing key that the v1 API
	// last accepted.
	signingKeys map[string]string
}

// New builds a Client. An empty baseURL falls back to Inngest Cloud.
func New(apiKey, baseURL, userAgent string) *Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &Client{
		APIKey:      apiKey,
		BaseURL:     strings.TrimRight(baseURL, "/"),
		HTTPClient:  &http.Client{Timeout: 60 * time.Second},
		UserAgent:   userAgent,
		MaxRetries:  5,
		MinBackoff:  time.Second,
		MaxBackoff:  30 * time.Second,
		Sleep:       sleepCtx,
		signingKeys: map[string]string{},
	}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// APIError is returned for a non-2xx response.
type APIError struct {
	Method     string
	Path       string
	StatusCode int
	// Code is the first machine-readable error code, such as "missing_field".
	Code string
	// Message joins the messages the API sent, or holds the raw body when it
	// sent no structured error.
	Message string
}

func (e *APIError) Error() string {
	msg := e.Message
	if e.Code != "" {
		msg = e.Code + ": " + msg
	}
	if msg == "" {
		msg = http.StatusText(e.StatusCode)
	}
	return fmt.Sprintf("the Inngest API answered %s %s with %d: %s", e.Method, e.Path, e.StatusCode, msg)
}

// IsNotFound reports whether err is a 404 from the API, or a lookup that found
// nothing.
func IsNotFound(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound
}

// IsStatus reports whether err is an API error with the given status.
func IsStatus(err error, status int) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == status
}

// notFound builds the error a list lookup returns when the item is missing.
func notFound(path, what string) error {
	return &APIError{Method: http.MethodGet, Path: path, StatusCode: http.StatusNotFound, Code: "not_found", Message: what + " not found"}
}

// request describes one API call.
type request struct {
	method string
	path   string
	query  url.Values
	env    string
	body   any
	// token overrides the API key, for v1 calls made with a signing key.
	token string
}

// envelope is the v2 response wrapper.
type envelope[T any] struct {
	Data T     `json:"data"`
	Page *Page `json:"page,omitempty"`
}

// Page is the v2 pagination block.
type Page struct {
	Cursor  string `json:"cursor,omitempty"`
	HasMore bool   `json:"hasMore"`
	Limit   int    `json:"limit,omitempty"`
}

func (c *Client) do(ctx context.Context, r request, out any) error {
	if c.KeyError != nil {
		return c.KeyError
	}
	var payload []byte
	if r.body != nil {
		b, err := json.Marshal(r.body)
		if err != nil {
			return fmt.Errorf("encoding request body: %w", err)
		}
		payload = b
	}
	target := c.BaseURL + r.path
	if len(r.query) > 0 {
		target += "?" + r.query.Encode()
	}
	token := c.APIKey
	if r.token != "" {
		token = r.token
	}

	idempotent := r.method != http.MethodPost
	sleep := c.Sleep
	if sleep == nil {
		sleep = sleepCtx
	}
	for attempt := 0; ; attempt++ {
		var reqBody io.Reader
		if payload != nil {
			reqBody = bytes.NewReader(payload)
		}
		req, err := http.NewRequestWithContext(ctx, r.method, target, reqBody)
		if err != nil {
			return fmt.Errorf("building request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/json")
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if r.env != "" {
			req.Header.Set(EnvHeader, r.env)
		}
		if c.UserAgent != "" {
			req.Header.Set("User-Agent", c.UserAgent)
		}

		res, err := c.HTTPClient.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			// A request that may have reached the API is only resent when
			// repeating it is harmless.
			if idempotent && attempt < c.MaxRetries {
				if serr := sleep(ctx, c.backoff(attempt, "")); serr != nil {
					return serr
				}
				continue
			}
			return fmt.Errorf("calling Inngest API %s %s: %w", r.method, r.path, err)
		}
		data, readErr := io.ReadAll(io.LimitReader(res.Body, 10<<20))
		_ = res.Body.Close()
		if readErr != nil {
			return fmt.Errorf("reading Inngest API response for %s %s: %w", r.method, r.path, readErr)
		}

		if retryable(res.StatusCode, idempotent) && attempt < c.MaxRetries {
			wait := c.backoff(attempt, res.Header.Get("Retry-After"))
			tflog.Debug(ctx, "Retrying Inngest API request", map[string]any{"method": r.method, "path": r.path, "status": res.StatusCode, "attempt": attempt + 1, "wait": wait.String()})
			if serr := sleep(ctx, wait); serr != nil {
				return serr
			}
			continue
		}
		if res.StatusCode >= http.StatusBadRequest {
			return parseError(r.method, r.path, res.StatusCode, data)
		}
		if out != nil && len(bytes.TrimSpace(data)) > 0 {
			if err := json.Unmarshal(data, out); err != nil {
				return fmt.Errorf("decoding Inngest API response for %s %s: %w", r.method, r.path, err)
			}
		}
		return nil
	}
}

// retryable reports whether a status is worth another try. A 429 means the
// request was refused, so it is always safe to resend. A 5xx may come after
// the change was made, so only requests that are safe to repeat are resent.
func retryable(status int, idempotent bool) bool {
	switch status {
	case http.StatusTooManyRequests:
		return true
	case http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return idempotent
	}
	return false
}

func (c *Client) backoff(attempt int, retryAfter string) time.Duration {
	if retryAfter != "" {
		if secs, err := strconv.Atoi(strings.TrimSpace(retryAfter)); err == nil && secs >= 0 {
			d := time.Duration(secs) * time.Second
			if d <= c.MaxBackoff {
				return d
			}
			return c.MaxBackoff
		}
		if t, err := http.ParseTime(retryAfter); err == nil {
			d := time.Until(t)
			if d < 0 {
				d = 0
			}
			if d <= c.MaxBackoff {
				return d
			}
			return c.MaxBackoff
		}
	}
	d := c.MinBackoff << attempt
	if d <= 0 || d > c.MaxBackoff {
		d = c.MaxBackoff
	}
	if d > 0 {
		// Up to 20% jitter, so parallel applies do not retry in step.
		d += time.Duration(rand.Int64N(int64(d)/5 + 1))
	}
	return d
}

// parseError reads the v2 error shape {"errors":[{"code","message"}]}, the
// {"error": "..."} shape, or a bare string body.
func parseError(method, path string, status int, data []byte) error {
	apiErr := &APIError{Method: method, Path: path, StatusCode: status}
	trimmed := bytes.TrimSpace(data)

	var v2 struct {
		Errors []struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"errors"`
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if len(trimmed) > 0 && trimmed[0] == '{' && json.Unmarshal(trimmed, &v2) == nil {
		var msgs []string
		for _, e := range v2.Errors {
			if apiErr.Code == "" {
				apiErr.Code = e.Code
			}
			if e.Message != "" {
				msgs = append(msgs, e.Message)
			}
		}
		switch {
		case len(msgs) > 0:
			apiErr.Message = strings.Join(msgs, "; ")
		case v2.Error != "":
			apiErr.Message = v2.Error
		case v2.Message != "":
			apiErr.Message = v2.Message
		}
	}
	if apiErr.Message == "" && apiErr.Code == "" {
		var s string
		if json.Unmarshal(trimmed, &s) == nil {
			apiErr.Message = s
		} else {
			apiErr.Message = string(trimmed)
		}
	}
	if len(apiErr.Message) > maxErrorBody {
		apiErr.Message = apiErr.Message[:maxErrorBody] + "..."
	}
	return apiErr
}

// listAll walks every page of a v2 list endpoint.
func listAll[T any](ctx context.Context, c *Client, path, env string, extra url.Values, limit int) ([]T, error) {
	var all []T
	cursor := ""
	seen := map[string]bool{}
	for {
		q := url.Values{}
		for k, v := range extra {
			q[k] = v
		}
		q.Set("limit", strconv.Itoa(limit))
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		var out envelope[[]T]
		if err := c.do(ctx, request{method: http.MethodGet, path: path, query: q, env: env}, &out); err != nil {
			return nil, err
		}
		all = append(all, out.Data...)
		if out.Page == nil || !out.Page.HasMore || out.Page.Cursor == "" {
			return all, nil
		}
		if seen[out.Page.Cursor] {
			return nil, fmt.Errorf("the Inngest API returned the cursor %q twice for GET %s; stopping to avoid a loop", out.Page.Cursor, path)
		}
		seen[out.Page.Cursor] = true
		cursor = out.Page.Cursor
	}
}
