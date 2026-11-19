package client

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

// App is the v2App schema.
type App struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	AppVersion    string   `json:"appVersion,omitempty"`
	ArchivedAt    string   `json:"archivedAt,omitempty"`
	CreatedAt     string   `json:"createdAt,omitempty"`
	FunctionCount int64    `json:"functionCount,omitempty"`
	IsArchived    bool     `json:"isArchived,omitempty"`
	Method        string   `json:"method,omitempty"`
	LatestSync    *AppSync `json:"latestSync,omitempty"`
}

// AppSync is the v2AppSync schema.
type AppSync struct {
	AppVersion  string `json:"appVersion,omitempty"`
	Error       string `json:"error,omitempty"`
	Framework   string `json:"framework,omitempty"`
	SDKLanguage string `json:"sdkLanguage,omitempty"`
	SDKVersion  string `json:"sdkVersion,omitempty"`
	Status      string `json:"status,omitempty"`
	SyncedAt    string `json:"syncedAt,omitempty"`
	URL         string `json:"url,omitempty"`
}

// appPageLimit is the largest page GET /v2/apps allows.
const appPageLimit = 100

// ListApps lists the apps of an environment (GET /v2/apps). With archived
// set it lists archived apps instead of active ones.
func (c *Client) ListApps(ctx context.Context, env string, archived bool) ([]App, error) {
	q := url.Values{}
	if archived {
		q.Set("archived", strconv.FormatBool(true))
	}
	return listAll[App](ctx, c, "/v2/apps", env, q, appPageLimit)
}

// GetApp fetches one app by its user-defined ID (GET /v2/apps/{appId}).
func (c *Client) GetApp(ctx context.Context, env, id string) (*App, error) {
	var out envelope[App]
	if err := c.do(ctx, request{method: http.MethodGet, path: "/v2/apps/" + url.PathEscape(id), env: env}, &out); err != nil {
		return nil, err
	}
	return &out.Data, nil
}
