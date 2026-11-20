package client

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-log/tflogtest"
)

func TestEnvironmentTypeOmission(t *testing.T) {
	c := clientWithHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			_, _ = io.WriteString(w, `{"data":{"id":"staging-123","name":"staging"}}`)
		} else {
			_, _ = io.WriteString(w, `{"data":[{"id":"production-123","name":"production"},{"id":"staging-123","name":"staging"},{"id":"branch-123","name":"feature/x","type":"BRANCH"}]}`)
		}
	}))
	created, err := c.CreateEnv(context.Background(), CreateEnvRequest{Name: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	if created.Type != "" || created.IsProduction() {
		t.Fatalf("custom environment misclassified: %+v", created)
	}
	envs, err := c.ListEnvs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(envs) != 3 || envs[0].Type != "" || !envs[0].IsProduction() || envs[1].Type != "" || envs[1].IsProduction() || envs[2].Type != EnvTypeBranch {
		t.Fatalf("environments: %+v", envs)
	}
}

func TestEnvironmentProtection(t *testing.T) {
	for _, tc := range []struct {
		name, typ string
		protected bool
	}{
		{"production", "", true}, {"PrOdUcTiOn", "", true}, {"primary", EnvTypeProduction, true},
		{"staging", "", false}, {"production-copy", "", false}, {"production", EnvTypeBranch, false}, {"staging", EnvTypeTest, false},
	} {
		t.Run(tc.name+tc.typ, func(t *testing.T) {
			if got := (&Env{Name: tc.name, Type: tc.typ}).IsProduction(); got != tc.protected {
				t.Fatalf("protected=%v want=%v", got, tc.protected)
			}
		})
	}
}

func TestEnvironmentScopedCallsAlwaysSendHeader(t *testing.T) {
	for _, env := range []string{"", "staging", "feature/x"} {
		t.Run("env="+env, func(t *testing.T) {
			want := env
			if want == "" {
				want = "production"
			}
			calls := 0
			c := clientWithHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Header.Get(EnvHeader) != want {
					t.Errorf("%s %s: env=%q want=%q", r.Method, r.URL.Path, r.Header.Get(EnvHeader), want)
				}
				if r.Method == http.MethodGet && (r.URL.Path == "/v2/apps" || r.URL.Path == "/v2/env/webhooks" || strings.HasPrefix(r.URL.Path, "/v2/keys/")) {
					_, _ = io.WriteString(w, `{"data":[]}`)
				} else {
					_, _ = io.WriteString(w, `{"data":{"id":"one"}}`)
				}
			}))
			c.SigningKey = "supplied-key"
			ctx := context.Background()
			checks := []func() error{
				func() error { _, err := c.ListEventKeys(ctx, env); return err },
				func() error { _, err := c.ListSigningKeys(ctx, env); return err },
				func() error { _, err := c.ListApps(ctx, env, false); return err },
				func() error { _, err := c.GetApp(ctx, env, "app"); return err },
				func() error { _, err := c.ListWebhooks(ctx, env); return err },
				func() error { _, err := c.CreateWebhook(ctx, env, CreateWebhookRequest{Name: "hook"}); return err },
				func() error { return c.UpdateWebhook(ctx, env, "hook", UpdateWebhookRequest{Name: "renamed"}) },
				func() error { return c.DeleteWebhook(ctx, env, "hook") },
			}
			for _, check := range checks {
				if err := check(); err != nil {
					t.Fatal(err)
				}
			}
			if calls != len(checks) {
				t.Fatalf("calls=%d want=%d", calls, len(checks))
			}
		})
	}
}

func TestV1ConfiguredSigningKeys(t *testing.T) {
	for _, hashed := range []bool{false, true} {
		t.Run(map[bool]string{false: "raw", true: "hashed"}[hashed], func(t *testing.T) {
			providerKey, overrideKey := "signkey-test-aabb", "signkey-test-ccdd"
			accepted := func(key string) string {
				if hashed {
					return HashSigningKey(key)
				}
				return key
			}
			s := &v1Server{accepted: accepted(providerKey)}
			c := clientWithHandler(s)
			c.SigningKey = providerKey
			ctx := context.Background()
			if err := c.UpdateWebhook(ctx, "production", "wh1", UpdateWebhookRequest{Name: "n"}); err != nil {
				t.Fatal(err)
			}
			// A different override in the same environment must bypass the provider cache.
			s.accepted = accepted(overrideKey)
			s.v1Tokens = nil
			if err := c.UpdateWebhook(ctx, "production", "wh1", UpdateWebhookRequest{Name: "n2"}, overrideKey); err != nil {
				t.Fatal(err)
			}
			if s.v1Tokens[0] != overrideKey {
				t.Fatal("override used another configuration's cached key")
			}
			if err := c.DeleteWebhook(ctx, "production", "wh1", overrideKey); err != nil {
				t.Fatal(err)
			}
			s.accepted = accepted(providerKey)
			if err := c.DeleteWebhook(ctx, "production", "wh1"); err != nil {
				t.Fatal(err)
			}
			s.accepted = "invalid"
			if err := c.DeleteWebhook(ctx, "production", "wh1", overrideKey); !IsStatus(err, http.StatusUnauthorized) {
				t.Fatalf("err=%v", err)
			}
			if s.keyLists != 0 {
				t.Fatalf("configured keys caused %d lookups", s.keyLists)
			}
		})
	}
}

func TestV1SigningKeyLookupForbidden(t *testing.T) {
	c := clientWithHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/keys/signing" || r.Header.Get(EnvHeader) != "production" {
			t.Errorf("unexpected request: %s env=%q", r.URL.Path, r.Header.Get(EnvHeader))
		}
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"errors":[{"code":"access_denied","message":"API key does not allow this operation"}]}`)
	}))
	want := "Inngest API keys cannot read signing keys. Set signing_key on the provider or the webhook (or INNGEST_SIGNING_KEY) to the environment's signing key from the Inngest dashboard."
	for _, call := range []func() error{
		func() error { return c.DeleteWebhook(context.Background(), "", "wh1") },
		func() error {
			return c.UpdateWebhook(context.Background(), "", "wh1", UpdateWebhookRequest{Name: "new"})
		},
	} {
		if err := call(); err == nil || err.Error() != want {
			t.Fatalf("err=%v", err)
		}
	}
}

func TestConfiguredSigningKeysAreRedacted(t *testing.T) {
	// Include a short nonstandard key and a key with a hashed form; both must be
	// redacted even when the server echoes them in the error code, not just message.
	for _, key := range []string{"tiny", "signkey-test-aabb"} {
		t.Run(key, func(t *testing.T) {
			providerKey := "provider-secret"
			c := clientWithHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") == "Bearer "+key && HashSigningKey(key) != "" {
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				w.WriteHeader(http.StatusBadRequest)
				msg := strings.Join([]string{key, HashSigningKey(key), providerKey}, " ")
				_ = json.NewEncoder(w).Encode(map[string]any{"errors": []map[string]string{{"code": msg, "message": msg}}})
			}))
			c.SigningKey = providerKey
			for i := 0; i < 2; i++ { // second call exercises the cached key
				var logs bytes.Buffer
				ctx := tflogtest.RootLogger(context.Background(), &logs)
				err := c.DeleteWebhook(ctx, "production", "wh", key)
				if err == nil {
					t.Fatal("expected error")
				}
				for _, secret := range []string{key, HashSigningKey(key), providerKey} {
					if secret != "" && strings.Contains(err.Error()+logs.String(), secret) {
						t.Fatal("error leaked signing key")
					}
				}
			}
		})
	}
}

// Exercise request/response behaviour without a TCP listener or network access.
type handlerTransport struct{ handler http.Handler }

func (tr handlerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body == nil {
		req.Body = http.NoBody
	}
	w := httptest.NewRecorder()
	tr.handler.ServeHTTP(w, req)
	return w.Result(), nil
}
func clientWithHandler(handler http.Handler) *Client {
	c := New("sk-inn-api-test", "http://mock.inngest.test", "test")
	c.HTTPClient = &http.Client{Transport: handlerTransport{handler}}
	c.MaxRetries = 0
	return c
}
