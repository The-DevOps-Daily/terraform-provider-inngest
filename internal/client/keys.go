package client

import (
	"context"
)

// Key is the v2EventKey and v2SigningKey schema; both have the same fields.
type Key struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Environment string `json:"environment"`
	Key         string `json:"key"`
	CreatedAt   string `json:"createdAt,omitempty"`
}

// keyPageLimit is the largest page the key endpoints allow.
const keyPageLimit = 100

// ListEventKeys lists the event keys of an environment (GET /v2/keys/events).
// An empty env lists the production environment's keys.
func (c *Client) ListEventKeys(ctx context.Context, env string) ([]Key, error) {
	return listAll[Key](ctx, c, "/v2/keys/events", env, nil, keyPageLimit)
}

// ListSigningKeys lists the signing keys of an environment
// (GET /v2/keys/signing). An empty env lists the production environment's
// keys.
func (c *Client) ListSigningKeys(ctx context.Context, env string) ([]Key, error) {
	return listAll[Key](ctx, c, "/v2/keys/signing", env, nil, keyPageLimit)
}
