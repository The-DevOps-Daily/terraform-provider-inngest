package client

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"

	"github.com/hashicorp/terraform-plugin-log/tflog"
)

// Filter types, from the v2FilterType enum.
const (
	FilterAllow = "ALLOW"
	FilterDeny  = "DENY"
)

// FilterTypes lists every v2FilterType value.
var FilterTypes = []string{FilterAllow, FilterDeny}

// EventFilter is the v2EventFilter schema.
type EventFilter struct {
	Events []string `json:"events,omitempty"`
	Filter string   `json:"filter,omitempty"`
}

// Webhook is the v2Webhook schema.
type Webhook struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	URL         string       `json:"url,omitempty"`
	Transform   string       `json:"transform,omitempty"`
	Response    string       `json:"response,omitempty"`
	EventFilter *EventFilter `json:"eventFilter,omitempty"`
	Environment string       `json:"environment,omitempty"`
	CreatedAt   string       `json:"createdAt,omitempty"`
	UpdatedAt   string       `json:"updatedAt,omitempty"`
}

// CreateWebhookRequest is the v2CreateWebhookRequest schema.
type CreateWebhookRequest struct {
	Name        string       `json:"name"`
	Transform   string       `json:"transform"`
	Response    string       `json:"response,omitempty"`
	EventFilter *EventFilter `json:"eventFilter,omitempty"`
}

// UpdateWebhookRequest is the body of PUT /v1/webhooks/{id} (the v1 Webhook
// schema). Only the name and the transform can change.
type UpdateWebhookRequest struct {
	Name      string `json:"name"`
	Transform string `json:"transform"`
}

// webhookPageLimit is the largest page GET /v2/env/webhooks allows.
const webhookPageLimit = 100

// CreateWebhook creates a webhook in an environment (POST /v2/env/webhooks).
func (c *Client) CreateWebhook(ctx context.Context, env string, req CreateWebhookRequest) (*Webhook, error) {
	var out envelope[Webhook]
	if err := c.do(ctx, request{method: http.MethodPost, path: "/v2/env/webhooks", env: env, body: req}, &out); err != nil {
		return nil, err
	}
	return &out.Data, nil
}

// ListWebhooks lists the webhooks of an environment (GET /v2/env/webhooks).
func (c *Client) ListWebhooks(ctx context.Context, env string) ([]Webhook, error) {
	return listAll[Webhook](ctx, c, "/v2/env/webhooks", env, nil, webhookPageLimit)
}

// GetWebhook finds one webhook by ID in the v2 list, which carries every
// field (the v1 read has no event filter, response or timestamps).
func (c *Client) GetWebhook(ctx context.Context, env, id string) (*Webhook, error) {
	hooks, err := c.ListWebhooks(ctx, env)
	if err != nil {
		return nil, err
	}
	for i := range hooks {
		if hooks[i].ID == id {
			return &hooks[i], nil
		}
	}
	return nil, &MissingError{What: "webhook " + id + " in environment " + env}
}

// UpdateWebhook changes a webhook's name and transform
// (PUT /v1/webhooks/{id}). The v2 API has no update endpoint yet.
func (c *Client) UpdateWebhook(ctx context.Context, env, id string, req UpdateWebhookRequest, signingKey ...string) error {
	return c.doV1(ctx, env, request{method: http.MethodPut, path: "/v1/webhooks/" + url.PathEscape(id), body: req}, signingKey...)
}

// DeleteWebhook deletes a webhook (DELETE /v1/webhooks/{id}). The v2 API has
// no delete endpoint yet.
func (c *Client) DeleteWebhook(ctx context.Context, env, id string, signingKey ...string) error {
	return c.doV1(ctx, env, request{method: http.MethodDelete, path: "/v1/webhooks/" + url.PathEscape(id)}, signingKey...)
}

// Cache by both environment and configured key so resource overrides and
// rotations never reuse a token selected for another configuration.
type signingKeyCacheID struct{ environment, configuredKey string }

// doV1 prefers the resource key, then the provider key. Only credentials with
// key access can use the v2 lookup fallback. Try raw and SDK-hashed forms.
func (c *Client) doV1(ctx context.Context, env string, r request, override ...string) error {
	if c.KeyError != nil {
		return c.KeyError
	}
	if env == "" {
		env = "production"
	}
	r.env = env
	key := c.SigningKey
	if len(override) > 0 && override[0] != "" {
		key = override[0]
	}
	r.secrets = append(r.secrets, key, HashSigningKey(key))
	cacheID := signingKeyCacheID{env, key}

	c.mu.Lock()
	cached := c.signingKeys[cacheID]
	c.mu.Unlock()
	if cached != "" {
		r.token = cached
		err := c.do(ctx, r, nil)
		if !IsStatus(err, http.StatusUnauthorized) {
			return err
		}
		c.mu.Lock()
		delete(c.signingKeys, cacheID)
		c.mu.Unlock()
	}

	keys := []Key{{Key: key}}
	if key == "" {
		var err error
		keys, err = c.ListSigningKeys(ctx, env)
		if IsStatus(err, http.StatusForbidden) {
			return errors.New("Inngest API keys cannot read signing keys. Set signing_key on the provider or the webhook (or INNGEST_SIGNING_KEY) to the environment's signing key from the Inngest dashboard.")
		}
		if err != nil {
			// A 404 here is about the key list, never a missing webhook.
			return fmt.Errorf("the v1 webhook API needs the environment's signing key, and listing signing keys for environment %q failed: %v", env, err)
		}
	}
	type candidate struct{ token, form string }
	var candidates []candidate
	for _, k := range keys {
		if k.Key == "" {
			continue
		}
		r.secrets = append(r.secrets, k.Key, HashSigningKey(k.Key))
		candidates = append(candidates, candidate{k.Key, "as given"})
		if h := HashSigningKey(k.Key); h != "" && h != k.Key {
			candidates = append(candidates, candidate{h, "hashed"})
		}
	}
	if len(candidates) == 0 {
		return fmt.Errorf("the v1 webhook API needs the environment's signing key, but GET /v2/keys/signing returned no keys for environment %q", env)
	}

	var lastErr error
	for _, cand := range candidates {
		r.token = cand.token
		err := c.do(ctx, r, nil)
		if IsStatus(err, http.StatusUnauthorized) {
			tflog.Debug(ctx, "Inngest v1 API refused a signing key", map[string]any{"environment": env, "form": cand.form})
			lastErr = err
			continue
		}
		// Any other answer means the key was accepted.
		tflog.Debug(ctx, "Inngest v1 API accepted a signing key", map[string]any{"environment": env, "form": cand.form})
		c.mu.Lock()
		c.signingKeys[cacheID] = cand.token
		c.mu.Unlock()
		return err
	}
	return errors.Join(fmt.Errorf("the v1 webhook API refused every signing key of environment %q", env), lastErr)
}

var signingKeyPrefix = regexp.MustCompile(`^signkey-\w+-`)

// HashSigningKey returns the form of a signing key that the Inngest SDKs send:
// the prefix, then the hex SHA-256 of the hex-decoded rest. It returns "" when
// the key is not in that format.
func HashSigningKey(key string) string {
	prefix := signingKeyPrefix.FindString(key)
	raw, err := hex.DecodeString(key[len(prefix):])
	if err != nil || len(raw) == 0 {
		return ""
	}
	sum := sha256.Sum256(raw)
	return prefix + hex.EncodeToString(sum[:])
}
