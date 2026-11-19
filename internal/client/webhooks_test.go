package client

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// v1Server answers GET /v2/keys/signing with the given keys and accepts v1
// calls only with the accepted token.
type v1Server struct {
	mu       sync.Mutex
	keys     string
	accepted string
	v1Tokens []string
	keyLists int
	lastEnv  string
	lastBody string
}

func (s *v1Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case r.URL.Path == "/v2/keys/signing":
		if r.Header.Get("Authorization") != "Bearer sk-inn-api-test" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		s.keyLists++
		_, _ = io.WriteString(w, `{"data":`+s.keys+`,"page":{"hasMore":false}}`)
	case strings.HasPrefix(r.URL.Path, "/v1/webhooks/"):
		tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		s.v1Tokens = append(s.v1Tokens, tok)
		s.lastEnv = r.Header.Get(EnvHeader)
		b, _ := io.ReadAll(r.Body)
		s.lastBody = string(b)
		if tok != s.accepted {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"error":"unauthorized"}`)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/missing") {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"error":"webhook not found"}`)
			return
		}
		_, _ = io.WriteString(w, `{"data":{"id":"wh1"}}`)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func TestV1UsesSigningKeyAndCachesIt(t *testing.T) {
	s := &v1Server{
		keys:     `[{"id":"k1","key":"signkey-staging-aaaa","environment":"staging"},{"id":"k2","key":"signkey-staging-bbbb","environment":"staging"}]`,
		accepted: "signkey-staging-bbbb",
	}
	srv := httptest.NewServer(s)
	defer srv.Close()
	c, _ := newTestClient(t, srv)

	err := c.UpdateWebhook(context.Background(), "staging", "wh1", UpdateWebhookRequest{Name: "n", Transform: "t"})
	if err != nil {
		t.Fatal(err)
	}
	// The first key, as given and hashed, is refused; the second works.
	if len(s.v1Tokens) != 3 || s.v1Tokens[0] != "signkey-staging-aaaa" || s.v1Tokens[1] != HashSigningKey("signkey-staging-aaaa") || s.v1Tokens[2] != "signkey-staging-bbbb" {
		t.Fatalf("tokens tried = %v", s.v1Tokens)
	}
	if s.lastEnv != "staging" || s.lastBody != `{"name":"n","transform":"t"}` {
		t.Fatalf("env=%q body=%s", s.lastEnv, s.lastBody)
	}

	// The working key is remembered for the environment.
	s.v1Tokens = nil
	if err := c.DeleteWebhook(context.Background(), "staging", "wh1"); err != nil {
		t.Fatal(err)
	}
	if s.keyLists != 1 || len(s.v1Tokens) != 1 {
		t.Fatalf("key lists=%d tokens=%v", s.keyLists, s.v1Tokens)
	}

	// A 404 is returned as is, and does not need another key.
	if err := c.DeleteWebhook(context.Background(), "staging", "missing"); !IsNotFound(err) {
		t.Fatalf("err = %v", err)
	}
}

func TestV1HashedKey(t *testing.T) {
	s := &v1Server{
		keys:     `[{"id":"k1","key":"signkey-prod-8f3a0b1c2d4e5f60","environment":"production"}]`,
		accepted: HashSigningKey("signkey-prod-8f3a0b1c2d4e5f60"),
	}
	srv := httptest.NewServer(s)
	defer srv.Close()
	c, _ := newTestClient(t, srv)
	if err := c.DeleteWebhook(context.Background(), "production", "wh1"); err != nil {
		t.Fatal(err)
	}
}

func TestV1CachedKeyRefreshedAfterRotation(t *testing.T) {
	s := &v1Server{keys: `[{"id":"k1","key":"signkey-staging-aaaa"}]`, accepted: "signkey-staging-aaaa"}
	srv := httptest.NewServer(s)
	defer srv.Close()
	c, _ := newTestClient(t, srv)
	if err := c.DeleteWebhook(context.Background(), "staging", "wh1"); err != nil {
		t.Fatal(err)
	}
	s.keys = `[{"id":"k2","key":"signkey-staging-cccc"}]`
	s.accepted = "signkey-staging-cccc"
	if err := c.DeleteWebhook(context.Background(), "staging", "wh1"); err != nil {
		t.Fatal(err)
	}
	if s.keyLists != 2 {
		t.Fatalf("key lists = %d", s.keyLists)
	}
}

func TestV1AllKeysRefused(t *testing.T) {
	s := &v1Server{keys: `[{"id":"k1","key":"signkey-staging-aaaa"}]`, accepted: "other"}
	srv := httptest.NewServer(s)
	defer srv.Close()
	c, _ := newTestClient(t, srv)
	err := c.DeleteWebhook(context.Background(), "staging", "wh1")
	if err == nil || !strings.Contains(err.Error(), "refused every signing key") || !IsStatus(err, http.StatusUnauthorized) {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "signkey-staging-aaaa") {
		t.Fatal("error message leaks a signing key")
	}
}

func TestV1NoKeys(t *testing.T) {
	s := &v1Server{keys: `[]`}
	srv := httptest.NewServer(s)
	defer srv.Close()
	c, _ := newTestClient(t, srv)
	if err := c.DeleteWebhook(context.Background(), "staging", "wh1"); err == nil || !strings.Contains(err.Error(), "returned no keys") {
		t.Fatalf("err = %v", err)
	}
}

func TestGetWebhookFromList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(EnvHeader) != "staging" {
			t.Errorf("env = %q", r.Header.Get(EnvHeader))
		}
		if r.URL.Query().Get("cursor") == "" {
			_, _ = io.WriteString(w, `{"data":[{"id":"a","name":"A"}],"page":{"cursor":"n","hasMore":true}}`)
			return
		}
		_, _ = io.WriteString(w, `{"data":[{"id":"b","name":"B","transform":"t","eventFilter":{"filter":"DENY","events":["x/*"]}}],"page":{"hasMore":false}}`)
	}))
	defer srv.Close()
	c, _ := newTestClient(t, srv)
	wh, err := c.GetWebhook(context.Background(), "staging", "b")
	if err != nil || wh.Name != "B" || wh.EventFilter.Filter != FilterDeny {
		t.Fatalf("wh=%+v err=%v", wh, err)
	}
	if _, err := c.GetWebhook(context.Background(), "staging", "c"); !IsNotFound(err) {
		t.Fatalf("err = %v", err)
	}
}
