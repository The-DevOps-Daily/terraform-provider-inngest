package client

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// These tests compare the client with Inngest's published OpenAPI specs. The
// specs are not committed (see testdata/README.md); `make spec` downloads
// them, and CI does so before the tests. Without them the tests skip.

type openAPISpec struct {
	Paths      map[string]map[string]json.RawMessage `json:"paths"`
	Components struct {
		Schemas map[string]struct {
			Properties map[string]json.RawMessage `json:"properties"`
			Enum       []string                   `json:"enum"`
		} `json:"schemas"`
	} `json:"components"`
}

func loadSpec(t *testing.T, name string) *openAPISpec {
	t.Helper()
	p := filepath.Join("..", "..", "testdata", "spec", name)
	b, err := os.ReadFile(p)
	if err != nil {
		if os.Getenv("INNGEST_REQUIRE_SPEC") != "" {
			t.Fatalf("reading %s: %v", p, err)
		}
		t.Skipf("%s not found; run `make spec` to download it", p)
	}
	var s openAPISpec
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatalf("parsing %s: %v", p, err)
	}
	return &s
}

// usedOperations are the operations the client calls, as they appear in each
// spec (the v2 spec's server URL already ends in /v2).
var usedOperations = map[string][]string{
	"v2.json": {
		"GET /envs", "POST /envs", "PATCH /envs/{id}",
		"GET /keys/events", "GET /keys/signing",
		"GET /env/webhooks", "POST /env/webhooks",
		"GET /apps", "GET /apps/{appId}",
	},
	"v1.json": {
		"PUT /v1/webhooks/{id}", "DELETE /v1/webhooks/{id}",
	},
}

func TestOperationsExistInSpec(t *testing.T) {
	for file, ops := range usedOperations {
		spec := loadSpec(t, file)
		for _, op := range ops {
			method, path, _ := strings.Cut(op, " ")
			methods, ok := spec.Paths[path]
			if !ok {
				t.Errorf("%s: path %s is not in the spec", file, path)
				continue
			}
			if _, ok := methods[strings.ToLower(method)]; !ok {
				t.Errorf("%s: %s is not in the spec", file, op)
			}
		}
	}
}

// TestFieldsExistInSpec checks every JSON field of the client's types against
// the spec schema it mirrors, so a renamed or removed field fails CI.
func TestFieldsExistInSpec(t *testing.T) {
	spec := loadSpec(t, "v2.json")
	types := map[string]any{
		"v2Env":                  Env{},
		"v2CreateEnvRequest":     CreateEnvRequest{},
		"V2PatchEnvBody":         PatchEnvRequest{},
		"v2EventKey":             Key{},
		"v2SigningKey":           Key{},
		"v2Webhook":              Webhook{},
		"v2CreateWebhookRequest": CreateWebhookRequest{},
		"v2EventFilter":          EventFilter{},
		"v2App":                  App{},
		"v2AppSync":              AppSync{},
		"v2Page":                 Page{},
	}
	for schemaName, v := range types {
		schema, ok := spec.Components.Schemas[schemaName]
		if !ok {
			t.Errorf("schema %s is not in the spec", schemaName)
			continue
		}
		rt := reflect.TypeOf(v)
		for i := 0; i < rt.NumField(); i++ {
			tag := rt.Field(i).Tag.Get("json")
			name, _, _ := strings.Cut(tag, ",")
			if name == "" || name == "-" {
				continue
			}
			if _, ok := schema.Properties[name]; !ok {
				t.Errorf("%s has no field %q (Go %s.%s)", schemaName, name, rt.Name(), rt.Field(i).Name)
			}
		}
	}
}

func TestEnumsMatchSpec(t *testing.T) {
	spec := loadSpec(t, "v2.json")
	for schemaName, want := range map[string][]string{
		"v2EnvType":    EnvTypes,
		"v2FilterType": FilterTypes,
	} {
		got := spec.Components.Schemas[schemaName].Enum
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%s enum = %v, client has %v", schemaName, got, want)
		}
	}
}

func TestV1WebhookUpdateBodyMatchesSpec(t *testing.T) {
	spec := loadSpec(t, "v1.json")
	schema := spec.Components.Schemas["Webhook"]
	for _, f := range []string{"name", "transform"} {
		if _, ok := schema.Properties[f]; !ok {
			t.Errorf("v1 Webhook schema has no %q", f)
		}
	}
}
