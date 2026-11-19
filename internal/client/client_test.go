package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// newTestClient returns a client for srv that never sleeps and records the
// waits it would have made.
func newTestClient(t *testing.T, srv *httptest.Server) (*Client, *[]time.Duration) {
	t.Helper()
	c := New("sk-inn-api-test", srv.URL+"/", "terraform-provider-inngest/test")
	var waits []time.Duration
	c.Sleep = func(_ context.Context, d time.Duration) error {
		waits = append(waits, d)
		return nil
	}
	return c, &waits
}

func TestRequestHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer sk-inn-api-test" {
			t.Errorf("Authorization = %q", got)
		}
		if got := r.Header.Get(EnvHeader); got != "feature/x" {
			t.Errorf("%s = %q", EnvHeader, got)
		}
		if got := r.Header.Get("User-Agent"); got != "terraform-provider-inngest/test" {
			t.Errorf("User-Agent = %q", got)
		}
		if r.URL.Path != "/v2/env/webhooks" || r.Method != http.MethodPost {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q", got)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["name"] != "Stripe" || body["transform"] != "function transform(e){return {name:'a',data:e}}" {
			t.Errorf("body = %v", body)
		}
		if _, ok := body["response"]; ok {
			t.Errorf("empty response should be left out: %v", body)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"data":{"id":"wh1","name":"Stripe","url":"https://inn.gs/e/abc","eventFilter":{"events":["stripe/*"]}},"metadata":{"fetchedAt":"2026-11-20T00:00:00Z","cachedUntil":null}}`)
	}))
	defer srv.Close()
	c, _ := newTestClient(t, srv)

	wh, err := c.CreateWebhook(context.Background(), "feature/x", CreateWebhookRequest{Name: "Stripe", Transform: "function transform(e){return {name:'a',data:e}}"})
	if err != nil {
		t.Fatal(err)
	}
	if wh.ID != "wh1" || wh.URL != "https://inn.gs/e/abc" || wh.EventFilter == nil || wh.EventFilter.Events[0] != "stripe/*" {
		t.Fatalf("webhook = %+v", wh)
	}
}

func TestNoEnvHeaderWhenEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := r.Header[http.CanonicalHeaderKey(EnvHeader)]; ok {
			t.Errorf("unexpected %s header", EnvHeader)
		}
		_, _ = io.WriteString(w, `{"data":[],"page":{"hasMore":false,"limit":100}}`)
	}))
	defer srv.Close()
	c, _ := newTestClient(t, srv)
	if _, err := c.ListEventKeys(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
}

func TestErrorParsing(t *testing.T) {
	cases := []struct {
		name, body, wantCode, wantMsg string
		status                        int
	}{
		{"v2 errors", `{"errors":[{"code":"missing_field","message":"Environment name is required"}]}`, "missing_field", "Environment name is required", 400},
		{"several errors", `{"errors":[{"code":"a","message":"one"},{"code":"b","message":"two"}]}`, "a", "one; two", 422},
		{"json string", `"Authentication failed"`, "", "Authentication failed", 401},
		{"plain text", "upstream timed out\n", "", "upstream timed out", 404},
		{"error field", `{"error":"webhook not found"}`, "", "webhook not found", 404},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer srv.Close()
			c, _ := newTestClient(t, srv)
			_, err := c.CreateEnv(context.Background(), CreateEnvRequest{Name: "x"})
			var apiErr *APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("err = %v, want *APIError", err)
			}
			if apiErr.StatusCode != tc.status || apiErr.Code != tc.wantCode || apiErr.Message != tc.wantMsg {
				t.Fatalf("got %+v", apiErr)
			}
			if !strings.Contains(err.Error(), "POST /v2/envs") || !strings.Contains(err.Error(), tc.wantMsg) {
				t.Fatalf("message %q lacks the request or the API's message", err.Error())
			}
			if strings.Contains(err.Error(), "sk-inn-api-test") {
				t.Fatal("error message leaks the API key")
			}
		})
	}
}

func TestLongErrorBodyIsTruncated(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, strings.Repeat("x", 5000))
	}))
	defer srv.Close()
	c, _ := newTestClient(t, srv)
	c.MaxRetries = 0
	_, err := c.ListEnvs(context.Background())
	var apiErr *APIError
	if !errors.As(err, &apiErr) || len(apiErr.Message) > maxErrorBody+3 {
		t.Fatalf("err = %v", err)
	}
}

func TestRetryOn429HonoursRetryAfter(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) <= 2 {
			w.Header().Set("Retry-After", "3")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"errors":[{"code":"rate_limited","message":"slow down"}]}`)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"data":{"id":"e1","name":"staging","type":"TEST"}}`)
	}))
	defer srv.Close()
	c, waits := newTestClient(t, srv)

	// A POST is resent after a 429, because the API refused it.
	env, err := c.CreateEnv(context.Background(), CreateEnvRequest{Name: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	if env.ID != "e1" || calls.Load() != 3 {
		t.Fatalf("env = %+v after %d calls", env, calls.Load())
	}
	if len(*waits) != 2 || (*waits)[0] != 3*time.Second {
		t.Fatalf("waits = %v", *waits)
	}
}

func TestRetryOn5xxOnlyForIdempotentRequests(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method == http.MethodGet && calls.Load() < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"errors":[{"code":"internal_error","message":"Something went wrong"}]}`)
			return
		}
		_, _ = io.WriteString(w, `{"data":[{"id":"e1","name":"production","type":"PRODUCTION"}],"page":{"hasMore":false}}`)
	}))
	defer srv.Close()
	c, waits := newTestClient(t, srv)

	envs, err := c.ListEnvs(context.Background())
	if err != nil || len(envs) != 1 || calls.Load() != 3 {
		t.Fatalf("envs=%v err=%v calls=%d", envs, err, calls.Load())
	}
	if len(*waits) != 2 || (*waits)[0] < time.Second || (*waits)[1] < 2*time.Second {
		t.Fatalf("backoff waits = %v", *waits)
	}

	// A create that failed with a 500 may have happened, so it is not resent.
	calls.Store(0)
	if _, err := c.CreateEnv(context.Background(), CreateEnvRequest{Name: "x"}); !IsStatus(err, 500) {
		t.Fatalf("err = %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("POST sent %d times after a 500", calls.Load())
	}
}

func TestRetriesGiveUp(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	c, waits := newTestClient(t, srv)
	c.MaxRetries = 3
	_, err := c.ListEnvs(context.Background())
	if !IsStatus(err, http.StatusTooManyRequests) || calls.Load() != 4 || len(*waits) != 3 {
		t.Fatalf("err=%v calls=%d waits=%v", err, calls.Load(), *waits)
	}
	if !strings.Contains(err.Error(), "Too Many Requests") {
		t.Fatalf("message = %q", err.Error())
	}
}

func TestBackoffCaps(t *testing.T) {
	c := New("k", "", "")
	if d := c.backoff(20, ""); d < c.MaxBackoff || d > c.MaxBackoff+c.MaxBackoff/5 {
		t.Fatalf("backoff(20) = %v", d)
	}
	if d := c.backoff(0, "600"); d != c.MaxBackoff {
		t.Fatalf("a long Retry-After is capped: %v", d)
	}
	if d := c.backoff(0, "0"); d != 0 {
		t.Fatalf("Retry-After 0 = %v", d)
	}
}

func TestPagination(t *testing.T) {
	var cursors []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("limit") != "250" {
			t.Errorf("limit = %q", r.URL.Query().Get("limit"))
		}
		cur := r.URL.Query().Get("cursor")
		cursors = append(cursors, cur)
		switch cur {
		case "":
			_, _ = io.WriteString(w, `{"data":[{"id":"1","name":"a"}],"page":{"cursor":"c1","hasMore":true,"limit":250}}`)
		case "c1":
			_, _ = io.WriteString(w, `{"data":[{"id":"2","name":"b"}],"page":{"cursor":"c2","hasMore":true,"limit":250}}`)
		default:
			_, _ = io.WriteString(w, `{"data":[{"id":"3","name":"c","isArchived":true}],"page":{"cursor":null,"hasMore":false,"limit":250}}`)
		}
	}))
	defer srv.Close()
	c, _ := newTestClient(t, srv)
	envs, err := c.ListEnvs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(envs) != 3 || !envs[2].IsArchived || strings.Join(cursors, ",") != ",c1,c2" {
		t.Fatalf("envs=%+v cursors=%v", envs, cursors)
	}

	got, err := c.GetEnv(context.Background(), "2")
	if err != nil || got.Name != "b" {
		t.Fatalf("GetEnv = %+v, %v", got, err)
	}
	if _, err := c.GetEnv(context.Background(), "nope"); !IsNotFound(err) {
		t.Fatalf("missing env: %v", err)
	}
}

func TestPaginationLoopIsCaught(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"data":[],"page":{"cursor":"same","hasMore":true}}`)
	}))
	defer srv.Close()
	c, _ := newTestClient(t, srv)
	if _, err := c.ListWebhooks(context.Background(), "production"); err == nil || !strings.Contains(err.Error(), "twice") {
		t.Fatalf("err = %v", err)
	}
}

func TestKeyError(t *testing.T) {
	c := New("", "http://127.0.0.1:1", "")
	c.KeyError = errors.New("missing key")
	if _, err := c.ListEnvs(context.Background()); err == nil || err.Error() != "missing key" {
		t.Fatalf("err = %v", err)
	}
	if err := c.DeleteWebhook(context.Background(), "production", "x"); err == nil || err.Error() != "missing key" {
		t.Fatalf("v1 err = %v", err)
	}
}

func TestNetworkErrorRetriedForGET(t *testing.T) {
	c := New("k", "http://127.0.0.1:1", "")
	var waits int
	c.Sleep = func(context.Context, time.Duration) error { waits++; return nil }
	c.MaxRetries = 2
	_, err := c.ListEnvs(context.Background())
	if err == nil || waits != 2 || !strings.Contains(err.Error(), "GET /v2/envs") {
		t.Fatalf("err=%v waits=%d", err, waits)
	}
	waits = 0
	if _, err := c.CreateEnv(context.Background(), CreateEnvRequest{Name: "x"}); err == nil || waits != 0 {
		t.Fatalf("POST network error retried %d times: %v", waits, err)
	}
}

func TestContextCancelStopsRetries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	c := New("k", srv.URL, "")
	c.MinBackoff = time.Hour
	c.MaxBackoff = time.Hour
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := c.ListEnvs(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
}

func TestHashSigningKey(t *testing.T) {
	if got := HashSigningKey("signkey-test-8f3a0b1c2d4e5f60"); got != "signkey-test-e40f724df8de01cb69758170de0789e9c30979ae1f40ed6ee40a7636fc12b032" {
		t.Fatalf("hash = %q", got)
	}
	if got := HashSigningKey("signkey-prod-not-hex"); got != "" {
		t.Fatalf("non-hex key hashed to %q", got)
	}
}

func TestPatchEnvBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if r.Method != http.MethodPatch || r.URL.Path != "/v2/envs/e%2F1" && r.URL.RawPath != "/v2/envs/e%2F1" {
			t.Errorf("got %s %s (%s)", r.Method, r.URL.Path, r.URL.RawPath)
		}
		if string(b) != `{"isArchived":false}` {
			t.Errorf("body = %s", b)
		}
		_, _ = io.WriteString(w, `{"data":{"id":"e/1","name":"staging","type":"TEST"}}`)
	}))
	defer srv.Close()
	c, _ := newTestClient(t, srv)
	f := false
	env, err := c.PatchEnv(context.Background(), "e/1", PatchEnvRequest{IsArchived: &f})
	if err != nil || env.IsArchived || env.Name != "staging" {
		t.Fatalf("env=%+v err=%v", env, err)
	}
}

func TestApps(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/apps":
			if r.URL.Query().Get("archived") != "true" {
				t.Errorf("archived = %q", r.URL.Query().Get("archived"))
			}
			_, _ = io.WriteString(w, `{"data":[{"id":"old","name":"old","isArchived":true}],"page":{"hasMore":false}}`)
		case "/v2/apps/my-app":
			_, _ = io.WriteString(w, `{"data":{"id":"my-app","name":"my-app","functionCount":2,"method":"SERVE","latestSync":{"sdkLanguage":"typescript","sdkVersion":"3.22.0","status":"success","url":"https://example.com/api/inngest"}}}`)
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"errors":[{"code":"not_found","message":"app not found"}]}`)
		}
	}))
	defer srv.Close()
	c, _ := newTestClient(t, srv)
	apps, err := c.ListApps(context.Background(), "", true)
	if err != nil || len(apps) != 1 || !apps[0].IsArchived {
		t.Fatalf("apps=%+v err=%v", apps, err)
	}
	app, err := c.GetApp(context.Background(), "production", "my-app")
	if err != nil || app.FunctionCount != 2 || app.LatestSync == nil || app.LatestSync.Status != "success" {
		t.Fatalf("app=%+v err=%v", app, err)
	}
	if _, err := c.GetApp(context.Background(), "production", "missing"); !IsNotFound(err) {
		t.Fatalf("err = %v", err)
	}
}
