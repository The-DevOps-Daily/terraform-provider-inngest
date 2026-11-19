package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/The-DevOps-Daily/terraform-provider-inngest/internal/client"
)

// fakeAPI is an in-memory Inngest REST API built from the published specs in
// testdata/ (see testdata/README.md). It follows what the specs and docs say:
//
//   - v2 takes the API key; v1 takes the environment's signing key, and an API
//     key on v1 gets a 401 ("API Key authentication (v2 endpoints only)").
//   - Responses are wrapped as {"data", "metadata", "page"}; errors are
//     {"errors": [{"code", "message"}]}, and a bad token gets the JSON string
//     "Authentication failed".
//   - Lists page with cursor/limit and reject limits outside the documented
//     range with 400 invalid_field_format.
//   - X-Inngest-Env picks the environment by name. The webhook endpoints need
//     it; the key and app endpoints fall back to production without it.
//   - Responses use protobuf JSON: default values (false, "", the ALLOW enum)
//     are left out, as grpc-gateway does.
type fakeAPI struct {
	mu    sync.Mutex
	seq   int
	clock time.Time

	envs     []*fakeEnv
	webhooks []*fakeWebhook
	apps     map[string][]map[string]any

	// pageSize caps every page, to exercise pagination.
	pageSize int
	// hideArchivedEnvs leaves archived environments out of GET /v2/envs.
	hideArchivedEnvs bool
	// throttle answers the next n calls of "METHOD /path" with a 429.
	throttle map[string]int
	// calls counts requests by "METHOD /path".
	calls map[string]int
	// envsAccountMissing and keysAccountMissing answer GET /v2/envs and
	// GET /v2/keys/* with the spec's 404 "account not found".
	envsAccountMissing, keysAccountMissing bool
}

type fakeEnv struct {
	ID, Name, Type, CreatedAt string
	Archived                  bool
	SigningKey                string
	EventKeys                 []string
}

type fakeWebhook struct {
	ID, Env, Name, URL, Transform, Response, CreatedAt, UpdatedAt string
	Filter                                                        *client.EventFilter
}

const fakeAPIKey = "sk-inn-api-test"

func newFakeAPI(t *testing.T) (*fakeAPI, string) {
	t.Helper()
	f := &fakeAPI{
		clock:    time.Date(2026, 11, 20, 1, 0, 0, 0, time.UTC),
		apps:     map[string][]map[string]any{},
		throttle: map[string]int{},
		calls:    map[string]int{},
	}
	f.addEnv("production", client.EnvTypeProduction)
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	return f, srv.URL
}

// skipWithoutTerraform skips the plan and apply tests when no Terraform CLI
// is available, so `go test` stays offline. CI installs Terraform and sets
// INNGEST_REQUIRE_TERRAFORM so the tests cannot silently skip there.
func skipWithoutTerraform(t *testing.T) {
	t.Helper()
	t.Setenv("CHECKPOINT_DISABLE", "1")
	for _, k := range []string{envAPIKey, envBaseURL, envEnvironment} {
		t.Setenv(k, "")
	}
	if os.Getenv("TF_ACC_TERRAFORM_PATH") != "" {
		return
	}
	if _, err := exec.LookPath("terraform"); err != nil {
		if os.Getenv("INNGEST_REQUIRE_TERRAFORM") != "" {
			t.Fatal("terraform CLI not found on PATH")
		}
		t.Skip("terraform CLI not found on PATH; skipping plan/apply test against the fake API")
	}
}

func fakeProviderConfig(baseURL string) string {
	return fmt.Sprintf("provider \"inngest\" {\n  api_key  = %q\n  base_url = %q\n}\n", fakeAPIKey, baseURL)
}

func fakeProviderConfigEnv(baseURL, env string) string {
	return fmt.Sprintf("provider \"inngest\" {\n  api_key     = %q\n  base_url    = %q\n  environment = %q\n}\n", fakeAPIKey, baseURL, env)
}

func (f *fakeAPI) nextID() string {
	f.seq++
	return fmt.Sprintf("00000000-0000-4000-8000-%012d", f.seq)
}

func (f *fakeAPI) now() string {
	f.clock = f.clock.Add(time.Second)
	return f.clock.Format(time.RFC3339)
}

// addEnv adds an environment with its own keys. Callers in tests hold no
// lock: Terraform is not running between steps.
func (f *fakeAPI) addEnv(name, typ string) *fakeEnv {
	id := f.nextID()
	e := &fakeEnv{
		ID: id, Name: name, Type: typ, CreatedAt: f.now(),
		SigningKey: fmt.Sprintf("signkey-%s-%012x", strings.ToLower(typ), f.seq),
		EventKeys:  []string{"evtkey-" + strconv.Itoa(f.seq)},
	}
	f.envs = append(f.envs, e)
	return e
}

func (f *fakeAPI) envByName(name string) *fakeEnv {
	for _, e := range f.envs {
		if e.Name == name {
			return e
		}
	}
	return nil
}

func (f *fakeAPI) envByID(id string) *fakeEnv {
	for _, e := range f.envs {
		if e.ID == id {
			return e
		}
	}
	return nil
}

func (f *fakeAPI) webhookByID(id string) *fakeWebhook {
	for _, w := range f.webhooks {
		if w.ID == id {
			return w
		}
	}
	return nil
}

func (f *fakeAPI) removeWebhook(id string) {
	for i, w := range f.webhooks {
		if w.ID == id {
			f.webhooks = append(f.webhooks[:i], f.webhooks[i+1:]...)
			return
		}
	}
}

func (f *fakeAPI) removeEnv(id string) {
	for i, e := range f.envs {
		if e.ID == id {
			f.envs = append(f.envs[:i], f.envs[i+1:]...)
			return
		}
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]any{"errors": []map[string]string{{"code": code, "message": msg}}})
}

func (f *fakeAPI) metadata() map[string]any {
	return map[string]any{"fetchedAt": f.clock.Format(time.RFC3339), "cachedUntil": nil}
}

func (f *fakeAPI) writeData(w http.ResponseWriter, status int, data any) {
	writeJSON(w, status, map[string]any{"data": data, "metadata": f.metadata()})
}

// writePage answers a list request with one page of rows. The cursor is the
// offset of the next row.
func (f *fakeAPI) writePage(w http.ResponseWriter, r *http.Request, rows []map[string]any, maxLimit, defLimit int) {
	limit := defLimit
	if s := r.URL.Query().Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > maxLimit {
			writeErr(w, http.StatusBadRequest, "invalid_field_format", fmt.Sprintf("Limit must be between 1 and %d", maxLimit))
			return
		}
		limit = n
	}
	pageLimit := limit
	if f.pageSize > 0 && f.pageSize < pageLimit {
		pageLimit = f.pageSize
	}
	start := 0
	if c := r.URL.Query().Get("cursor"); c != "" {
		n, err := strconv.Atoi(strings.TrimPrefix(c, "cur_"))
		if err != nil || n < 0 || n > len(rows) {
			writeErr(w, http.StatusBadRequest, "invalid_field_format", "Invalid cursor")
			return
		}
		start = n
	}
	end := min(start+pageLimit, len(rows))
	page := map[string]any{"hasMore": end < len(rows), "limit": limit}
	if end < len(rows) {
		page["cursor"] = "cur_" + strconv.Itoa(end)
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": rows[start:end], "metadata": f.metadata(), "page": page})
}

// omitDefaults drops zero values, as protobuf JSON does.
func omitDefaults(m map[string]any) map[string]any {
	for k, v := range m {
		switch x := v.(type) {
		case string:
			if x == "" {
				delete(m, k)
			}
		case bool:
			if !x {
				delete(m, k)
			}
		case nil:
			delete(m, k)
		}
	}
	return m
}

func (e *fakeEnv) json() map[string]any {
	m := omitDefaults(map[string]any{"id": e.ID, "name": e.Name, "type": e.Type, "createdAt": e.CreatedAt, "isArchived": e.Archived})
	if e.Type == client.EnvTypeProduction {
		delete(m, "type") // the enum's zero value
	}
	return m
}

func (wh *fakeWebhook) json() map[string]any {
	m := omitDefaults(map[string]any{
		"id": wh.ID, "name": wh.Name, "url": wh.URL, "transform": wh.Transform, "response": wh.Response,
		"environment": wh.Env, "createdAt": wh.CreatedAt, "updatedAt": wh.UpdatedAt,
	})
	if wh.Filter != nil {
		ef := map[string]any{"events": wh.Filter.Events}
		if wh.Filter.Filter != client.FilterAllow {
			ef["filter"] = wh.Filter.Filter
		}
		m["eventFilter"] = ef
	}
	return m
}

func (f *fakeAPI) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()

	route := r.Method + " " + r.URL.Path
	f.calls[route]++
	if n := f.throttle[route]; n > 0 {
		f.throttle[route] = n - 1
		w.Header().Set("Retry-After", "0")
		writeErr(w, http.StatusTooManyRequests, "rate_limited", "API rate limit exceeded")
		return
	}

	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if strings.HasPrefix(r.URL.Path, "/v1/") {
		f.serveV1(w, r, token)
		return
	}
	if token != fakeAPIKey {
		writeJSON(w, http.StatusUnauthorized, "Authentication failed")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/v2")
	if (f.envsAccountMissing && path == "/envs") || (f.keysAccountMissing && strings.HasPrefix(path, "/keys/")) {
		writeErr(w, http.StatusNotFound, "not_found", "Account not found")
		return
	}
	switch {
	case path == "/envs" && r.Method == http.MethodGet:
		var rows []map[string]any
		for _, e := range f.envs {
			if e.Archived && f.hideArchivedEnvs {
				continue
			}
			rows = append(rows, e.json())
		}
		f.writePage(w, r, rows, 250, 50)
	case path == "/envs" && r.Method == http.MethodPost:
		var body struct {
			Name string `json:"name"`
			ID   string `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid_request", "Invalid JSON body")
			return
		}
		if body.Name == "" {
			writeErr(w, http.StatusBadRequest, "missing_field", "Environment name is required")
			return
		}
		if f.envByName(body.Name) != nil {
			writeErr(w, http.StatusConflict, "already_exists", "Environment already exists")
			return
		}
		// The spec's example answers a create with type BRANCH.
		e := f.addEnv(body.Name, client.EnvTypeBranch)
		f.writeData(w, http.StatusCreated, e.json())
	case strings.HasPrefix(path, "/envs/") && r.Method == http.MethodPatch:
		e := f.envByID(strings.TrimPrefix(path, "/envs/"))
		if e == nil {
			writeErr(w, http.StatusNotFound, "not_found", "Environment not found")
			return
		}
		var body struct {
			IsArchived *bool `json:"isArchived"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid_request", "Invalid JSON body")
			return
		}
		if body.IsArchived != nil {
			if *body.IsArchived && e.Type == client.EnvTypeProduction {
				writeErr(w, http.StatusUnprocessableEntity, "invalid_operation", "The production environment cannot be archived")
				return
			}
			e.Archived = *body.IsArchived
		}
		f.writeData(w, http.StatusOK, e.json())
	case path == "/keys/events" || path == "/keys/signing":
		e := f.headerEnv(w, r, false)
		if e == nil {
			return
		}
		var rows []map[string]any
		if path == "/keys/signing" {
			rows = append(rows, map[string]any{"id": e.ID + "-sk", "name": "", "environment": e.Name, "key": e.SigningKey, "createdAt": e.CreatedAt})
		} else {
			for i, k := range e.EventKeys {
				rows = append(rows, map[string]any{"id": fmt.Sprintf("%s-ek%d", e.ID, i), "name": fmt.Sprintf("Key %d", i+1), "environment": e.Name, "key": k, "createdAt": e.CreatedAt})
			}
		}
		f.writePage(w, r, rows, 100, 20)
	case path == "/env/webhooks" && r.Method == http.MethodGet:
		e := f.headerEnv(w, r, true)
		if e == nil {
			return
		}
		var rows []map[string]any
		for _, wh := range f.webhooks {
			if wh.Env == e.Name {
				rows = append(rows, wh.json())
			}
		}
		f.writePage(w, r, rows, 100, 20)
	case path == "/env/webhooks" && r.Method == http.MethodPost:
		e := f.headerEnv(w, r, true)
		if e == nil {
			return
		}
		var body client.CreateWebhookRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid_request", "Invalid JSON body")
			return
		}
		if body.Name == "" {
			writeErr(w, http.StatusBadRequest, "missing_field", "Webhook name is required")
			return
		}
		if len(body.Name) > 128 {
			writeErr(w, http.StatusBadRequest, "invalid_field_format", "Webhook name must be at most 128 characters")
			return
		}
		if body.EventFilter != nil && body.EventFilter.Filter != "" && body.EventFilter.Filter != client.FilterAllow && body.EventFilter.Filter != client.FilterDeny {
			writeErr(w, http.StatusBadRequest, "invalid_field_format", "Invalid filter type")
			return
		}
		now := f.now()
		wh := &fakeWebhook{
			ID: f.nextID(), Env: e.Name, Name: body.Name, Transform: body.Transform, Response: body.Response,
			URL: fmt.Sprintf("https://inn.gs/e/token%d", f.seq), CreatedAt: now, UpdatedAt: now,
		}
		if body.EventFilter != nil && len(body.EventFilter.Events) > 0 {
			ff := *body.EventFilter
			if ff.Filter == "" {
				ff.Filter = client.FilterAllow
			}
			wh.Filter = &ff
		}
		f.webhooks = append(f.webhooks, wh)
		f.writeData(w, http.StatusCreated, wh.json())
	case path == "/apps" && r.Method == http.MethodGet:
		e := f.headerEnv(w, r, false)
		if e == nil {
			return
		}
		archived := r.URL.Query().Get("archived") == "true"
		var rows []map[string]any
		for _, a := range f.apps[e.Name] {
			if (a["isArchived"] == true) == archived {
				rows = append(rows, omitDefaults(a))
			}
		}
		f.writePage(w, r, rows, 100, 20)
	case strings.HasPrefix(path, "/apps/") && r.Method == http.MethodGet:
		e := f.headerEnv(w, r, false)
		if e == nil {
			return
		}
		id := strings.TrimPrefix(path, "/apps/")
		for _, a := range f.apps[e.Name] {
			if a["id"] == id {
				f.writeData(w, http.StatusOK, a)
				return
			}
		}
		writeErr(w, http.StatusNotFound, "not_found", "App not found")
	default:
		writeErr(w, http.StatusNotFound, "not_found", "Not found")
	}
}

// headerEnv resolves X-Inngest-Env. Without the header, required endpoints
// answer 400 and the others use production.
func (f *fakeAPI) headerEnv(w http.ResponseWriter, r *http.Request, required bool) *fakeEnv {
	name := r.Header.Get(client.EnvHeader)
	if name == "" {
		if required {
			writeErr(w, http.StatusBadRequest, "missing_field", "X-Inngest-Env header is required")
			return nil
		}
		name = "production"
	}
	e := f.envByName(name)
	if e == nil {
		writeErr(w, http.StatusNotFound, "not_found", "Environment not found")
		return nil
	}
	return e
}

// serveV1 handles the v1 webhook endpoints. The signing key picks the
// environment; v1 has no documented error schema, so errors are
// {"error": "..."}.
func (f *fakeAPI) serveV1(w http.ResponseWriter, r *http.Request, token string) {
	var env *fakeEnv
	for _, e := range f.envs {
		if token == e.SigningKey || token == client.HashSigningKey(e.SigningKey) {
			env = e
			break
		}
	}
	if env == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	if h := r.Header.Get(client.EnvHeader); h != "" && h != env.Name {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "signing key does not match the environment"})
		return
	}
	id, ok := strings.CutPrefix(r.URL.Path, "/v1/webhooks/")
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	wh := f.webhookByID(id)
	if wh == nil || wh.Env != env.Name {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "webhook not found"})
		return
	}
	v1 := func() map[string]any {
		return map[string]any{"id": wh.ID, "name": wh.Name, "transform": wh.Transform, "url": wh.URL}
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"data": v1()})
	case http.MethodPut:
		var body struct {
			Name      string `json:"name"`
			Transform string `json:"transform"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
			return
		}
		if body.Name == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Webhook name cannot be empty"})
			return
		}
		wh.Name, wh.Transform, wh.UpdatedAt = body.Name, body.Transform, f.now()
		writeJSON(w, http.StatusOK, map[string]any{"data": v1()})
	case http.MethodDelete:
		f.removeWebhook(id)
		writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"id": id}})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}
