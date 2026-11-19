package client

import (
	"context"
	"net/http"
	"net/url"
)

// Environment types, from the v2EnvType enum.
const (
	EnvTypeProduction = "PRODUCTION"
	EnvTypeTest       = "TEST"
	EnvTypeBranch     = "BRANCH"
)

// EnvTypes lists every v2EnvType value.
var EnvTypes = []string{EnvTypeProduction, EnvTypeTest, EnvTypeBranch}

// Env is the v2Env schema.
type Env struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Type       string `json:"type,omitempty"`
	CreatedAt  string `json:"createdAt,omitempty"`
	IsArchived bool   `json:"isArchived,omitempty"`
}

// CreateEnvRequest is the v2CreateEnvRequest schema. The spec also has an
// optional id field with no description; the provider does not send it.
type CreateEnvRequest struct {
	Name string `json:"name"`
}

// PatchEnvRequest is the V2PatchEnvBody schema.
type PatchEnvRequest struct {
	IsArchived *bool `json:"isArchived,omitempty"`
}

// envPageLimit is the largest page GET /v2/envs allows.
const envPageLimit = 250

// ListEnvs returns every environment of the account (GET /v2/envs).
func (c *Client) ListEnvs(ctx context.Context) ([]Env, error) {
	envs, err := listAll[Env](ctx, c, "/v2/envs", "", nil, envPageLimit)
	for i := range envs {
		envs[i].setDefaults()
	}
	return envs, err
}

// setDefaults fills in the type when the API leaves it out. The service
// speaks protobuf JSON, which omits an enum's zero value, and the zero value
// of v2EnvType is PRODUCTION (the spec's default).
func (e *Env) setDefaults() {
	if e.Type == "" {
		e.Type = EnvTypeProduction
	}
}

// GetEnv finds one environment by ID. The API has no read-one endpoint, so
// this walks the list and returns a not-found error when the ID is missing.
func (c *Client) GetEnv(ctx context.Context, id string) (*Env, error) {
	envs, err := c.ListEnvs(ctx)
	if err != nil {
		return nil, err
	}
	for i := range envs {
		if envs[i].ID == id {
			return &envs[i], nil
		}
	}
	return nil, &MissingError{What: "environment " + id}
}

// CreateEnv creates a custom environment (POST /v2/envs).
func (c *Client) CreateEnv(ctx context.Context, req CreateEnvRequest) (*Env, error) {
	var out envelope[Env]
	if err := c.do(ctx, request{method: http.MethodPost, path: "/v2/envs", body: req}, &out); err != nil {
		return nil, err
	}
	out.Data.setDefaults()
	return &out.Data, nil
}

// PatchEnv changes an environment (PATCH /v2/envs/{id}). Only the archived
// flag can change. The answer is returned as sent: callers keep the other
// fields from what they already know.
func (c *Client) PatchEnv(ctx context.Context, id string, req PatchEnvRequest) (*Env, error) {
	var out envelope[Env]
	if err := c.do(ctx, request{method: http.MethodPatch, path: "/v2/envs/" + url.PathEscape(id), body: req}, &out); err != nil {
		return nil, err
	}
	return &out.Data, nil
}
