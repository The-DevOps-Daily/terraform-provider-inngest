package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/The-DevOps-Daily/terraform-provider-inngest/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	frameworkprovider "github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	testresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestEnvironmentDeleteProtection(t *testing.T) {
	for _, tc := range []struct {
		name, typ string
		protected bool
	}{
		{"production", "", true}, {"PrOdUcTiOn", "", true}, {"main", "PRODUCTION", true},
		{"custom", "", false}, {"production", "BRANCH", false},
	} {
		t.Run(tc.name+tc.typ, func(t *testing.T) {
			api := newFakeAPIState()
			e := api.addEnv(tc.name, tc.typ)
			r := NewEnvironmentResource().(*environmentResource)
			r.data = &providerData{client: fakeClient(api)}
			ctx := context.Background()
			var schema resource.SchemaResponse
			r.Schema(ctx, resource.SchemaRequest{}, &schema)
			state := tfsdk.State{Schema: schema.Schema}
			m := environmentResourceModel{ID: types.StringValue(e.ID), Name: types.StringValue(e.Name), Type: optionalString(tc.typ), Archived: types.BoolValue(false), CreatedAt: types.StringValue(e.CreatedAt)}
			if diags := state.Set(ctx, m); diags.HasError() {
				t.Fatal(diags)
			}
			var resp resource.DeleteResponse
			r.Delete(ctx, resource.DeleteRequest{State: state}, &resp)
			if resp.Diagnostics.HasError() {
				t.Fatal(resp.Diagnostics)
			}
			if e.Archived == tc.protected {
				t.Fatalf("archived=%v protected=%v", e.Archived, tc.protected)
			}
			want := 1
			if tc.protected {
				want = 0
			}
			if api.calls["PATCH /v2/envs/"+e.ID] != want {
				t.Fatal("unexpected archive calls")
			}
		})
	}
}

func TestEnvironmentRefreshClearsOldProductionDefault(t *testing.T) {
	m := environmentResourceModel{Type: types.StringValue(client.EnvTypeProduction)}
	mapEnvironment(&client.Env{ID: "custom-id", Name: "custom"}, &m)
	if !m.Type.IsNull() {
		t.Fatal("refresh retained incorrect old production type")
	}
}

func TestMockKeysForbidden(t *testing.T) {
	skipWithoutTerraform(t)
	_, url := newFakeAPI(t)
	for _, kind := range []string{"event_keys", "signing_keys"} {
		t.Run(kind, func(t *testing.T) {
			testresource.UnitTest(t, testresource.TestCase{
				ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
				Steps: []testresource.TestStep{{
					Config:      fakeProviderConfig(url) + fmt.Sprintf(`data "inngest_%s" "keys" {}`, kind),
					ExpectError: regexp.MustCompile(`(?s)API\s+keys\s+cannot\s+read\s+keys.*credential\s+with\s+key\s+access`),
				}},
			})
		})
	}
}

func TestMockWebhookConfiguredSigningKey(t *testing.T) {
	for _, source := range []string{"provider", "environment", "resource"} {
		t.Run(source, func(t *testing.T) {
			skipWithoutTerraform(t)
			api, url := newFakeAPI(t)
			key := api.envByName("production").SigningKey
			p := fakeProviderConfig(url)
			extra := ""
			switch source {
			case "provider":
				t.Setenv(envSigningKey, "wrong-env-key")
				p = strings.Replace(p, "provider \"inngest\" {", fmt.Sprintf("provider \"inngest\" {\n signing_key = %q", key), 1)
			case "environment":
				t.Setenv(envSigningKey, key)
			case "resource":
				t.Setenv(envSigningKey, "wrong-env-key")
				p = strings.Replace(p, "provider \"inngest\" {", "provider \"inngest\" {\n signing_key = \"wrong-provider-key\"", 1)
				extra = fmt.Sprintf("signing_key = %q", key)
			}
			var id string
			testresource.UnitTest(t, testresource.TestCase{
				ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
				CheckDestroy: func(*terraform.State) error {
					if len(api.webhooks) != 0 {
						return fmt.Errorf("webhook remains after destroy")
					}
					if api.calls["GET /v2/keys/signing"] != 0 {
						return fmt.Errorf("configured key triggered a lookup")
					}
					if api.calls["PUT /v1/webhooks/"+id] != 1 || api.calls["DELETE /v1/webhooks/"+id] != 1 {
						return fmt.Errorf("expected one v1 update and delete")
					}
					return nil
				},
				Steps: []testresource.TestStep{
					{Config: p + webhookConfig("Stripe", stripeTransform, extra), Check: func(s *terraform.State) error {
						id = s.RootModule().Resources["inngest_webhook.stripe"].Primary.ID
						return nil
					}},
					{Config: p + webhookConfig("Renamed", stripeTransform, extra), Check: testresource.TestCheckResourceAttr("inngest_webhook.stripe", "name", "Renamed")},
				},
			})
		})
	}
}

func TestMockDefaultEnvironmentDataSources(t *testing.T) {
	skipWithoutTerraform(t)
	api, url := newFakeAPI(t)
	api.webhooks = append(api.webhooks, &fakeWebhook{ID: "wh", Env: "production", Name: "production hook"})
	api.apps["production"] = []map[string]any{{"id": "app", "name": "production app"}}
	testresource.UnitTest(t, testresource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []testresource.TestStep{{
			Config: fakeProviderConfig(url) + `
data "inngest_webhooks" "default" {}
data "inngest_apps" "default" {}
data "inngest_app" "default" { id = "app" }
`,
			Check: testresource.ComposeAggregateTestCheckFunc(
				testresource.TestCheckResourceAttr("data.inngest_webhooks.default", "webhooks.0.name", "production hook"),
				testresource.TestCheckResourceAttr("data.inngest_apps.default", "apps.0.name", "production app"),
				testresource.TestCheckResourceAttr("data.inngest_app.default", "name", "production app"),
			),
		}},
	})
}

// Direct handler transport keeps provider unit tests independent of sockets.
type fakeTransport struct{ api *fakeAPI }

func (tr fakeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body == nil {
		req.Body = http.NoBody
	}
	w := httptest.NewRecorder()
	tr.api.serve(w, req)
	return w.Result(), nil
}
func fakeClient(api *fakeAPI) *client.Client {
	c := client.New(fakeAPIKey, "http://mock.inngest.test", "test")
	c.HTTPClient = &http.Client{Transport: fakeTransport{api}}
	c.MaxRetries = 0
	return c
}

func TestSigningKeyProviderConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name, fromEnv, want string
		configured          types.String
		wantError           bool
	}{
		{name: "unset", configured: types.StringNull()},
		{name: "environment", fromEnv: "env-key", want: "env-key", configured: types.StringNull()},
		{name: "explicit overrides environment", fromEnv: "env-key", want: "explicit-key", configured: types.StringValue("explicit-key")},
		{name: "explicit empty overrides environment", fromEnv: "env-key", configured: types.StringValue("")},
		{name: "unknown does not use stale environment key", fromEnv: "env-key", configured: types.StringUnknown(), wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(envSigningKey, tc.fromEnv)
			ctx := context.Background()
			p := New("test")()
			var schema frameworkprovider.SchemaResponse
			p.Schema(ctx, frameworkprovider.SchemaRequest{}, &schema)
			if !schema.Schema.Attributes["signing_key"].IsSensitive() {
				t.Fatal("signing_key must be sensitive")
			}
			state := tfsdk.State{Schema: schema.Schema}
			config := inngestProviderModel{APIKey: types.StringValue(fakeAPIKey), SigningKey: tc.configured, BaseURL: types.StringNull(), Environment: types.StringNull()}
			if diags := state.Set(ctx, config); diags.HasError() {
				t.Fatal(diags)
			}
			var resp frameworkprovider.ConfigureResponse
			p.Configure(ctx, frameworkprovider.ConfigureRequest{Config: tfsdk.Config{Schema: schema.Schema, Raw: state.Raw}}, &resp)
			if resp.Diagnostics.HasError() != tc.wantError {
				t.Fatal(resp.Diagnostics)
			}
			if !tc.wantError && resp.ResourceData.(*providerData).client.SigningKey != tc.want {
				t.Fatal("incorrect signing key precedence")
			}
		})
	}
}

func TestKeysDataSourceForbiddenDiagnostics(t *testing.T) {
	for _, factory := range []func() datasource.DataSource{NewEventKeysDataSource, NewSigningKeysDataSource} {
		d := factory().(*keysDataSource)
		t.Run(d.typeSuffix, func(t *testing.T) {
			ctx := context.Background()
			d.data = &providerData{client: fakeClient(newFakeAPIState())}
			var schema datasource.SchemaResponse
			d.Schema(ctx, datasource.SchemaRequest{}, &schema)
			state := tfsdk.State{Schema: schema.Schema}
			if diags := state.Set(ctx, keysModel{Environment: types.StringNull(), Name: types.StringNull()}); diags.HasError() {
				t.Fatal(diags)
			}
			var resp datasource.ReadResponse
			d.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: schema.Schema, Raw: state.Raw}}, &resp)
			if !resp.Diagnostics.HasError() {
				t.Fatal("expected access diagnostic")
			}
			detail := resp.Diagnostics[0].Detail()
			if !strings.Contains(detail, "API keys cannot read keys") || !strings.Contains(detail, "credential with key access") {
				t.Fatal(detail)
			}
		})
	}
}

func TestWebhookConfiguredKeyLifecycle(t *testing.T) {
	for _, override := range []bool{false, true} {
		t.Run(fmt.Sprintf("override=%v", override), func(t *testing.T) {
			ctx := context.Background()
			api := newFakeAPIState()
			key := api.envByName("production").SigningKey
			c := fakeClient(api)
			c.SigningKey = key
			r := NewWebhookResource().(*webhookResource)
			r.data = &providerData{client: c}
			var schema resource.SchemaResponse
			r.Schema(ctx, resource.SchemaRequest{}, &schema)
			if !schema.Schema.Attributes["signing_key"].IsSensitive() {
				t.Fatal("signing_key must be sensitive")
			}
			model := webhookResourceModel{
				Name: types.StringValue("original"), Transform: types.StringValue(stripeTransform),
				Environment: types.StringValue("production"), SigningKey: types.StringNull(),
				EventFilter: types.ObjectNull(eventFilterAttrTypes),
			}
			if override {
				model.SigningKey = types.StringValue(key)
				c.SigningKey = "wrong-provider-key"
			}
			plan := tfsdk.Plan{Schema: schema.Schema}
			if diags := plan.Set(ctx, model); diags.HasError() {
				t.Fatal(diags)
			}
			created := resource.CreateResponse{State: tfsdk.State{Schema: schema.Schema}}
			r.Create(ctx, resource.CreateRequest{Plan: plan}, &created)
			if created.Diagnostics.HasError() {
				t.Fatal(created.Diagnostics)
			}
			if diags := created.State.Get(ctx, &model); diags.HasError() {
				t.Fatal(diags)
			}
			model.Name = types.StringValue("renamed")
			if diags := plan.Set(ctx, model); diags.HasError() {
				t.Fatal(diags)
			}
			updated := resource.UpdateResponse{State: tfsdk.State{Schema: schema.Schema}}
			r.Update(ctx, resource.UpdateRequest{Plan: plan, State: created.State}, &updated)
			if updated.Diagnostics.HasError() {
				t.Fatal(updated.Diagnostics)
			}
			if api.webhooks[0].Name != "renamed" {
				t.Fatal("webhook was not updated")
			}
			var deleted resource.DeleteResponse
			r.Delete(ctx, resource.DeleteRequest{State: updated.State}, &deleted)
			if deleted.Diagnostics.HasError() {
				t.Fatal(deleted.Diagnostics)
			}
			if len(api.webhooks) != 0 || api.calls["GET /v2/keys/signing"] != 0 {
				t.Fatal("delete failed or key lookup used")
			}
		})
	}
}

func TestEnvironmentLifecycleWithoutType(t *testing.T) {
	ctx := context.Background()
	api := newFakeAPIState()
	r := NewEnvironmentResource().(*environmentResource)
	r.data = &providerData{client: fakeClient(api)}
	var schema resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schema)
	plan := tfsdk.Plan{Schema: schema.Schema}
	model := environmentResourceModel{Name: types.StringValue("staging"), Type: types.StringUnknown(), Archived: types.BoolUnknown()}
	if diags := plan.Set(ctx, model); diags.HasError() {
		t.Fatal(diags)
	}
	created := resource.CreateResponse{State: tfsdk.State{Schema: schema.Schema}}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &created)
	if created.Diagnostics.HasError() {
		t.Fatal(created.Diagnostics)
	}
	if diags := created.State.Get(ctx, &model); diags.HasError() {
		t.Fatal(diags)
	}
	if !model.Type.IsNull() {
		t.Fatal("created custom environment has an inferred type")
	}
	read := resource.ReadResponse{State: created.State}
	r.Read(ctx, resource.ReadRequest{State: created.State}, &read)
	if read.Diagnostics.HasError() {
		t.Fatal(read.Diagnostics)
	}
	var deleted resource.DeleteResponse
	r.Delete(ctx, resource.DeleteRequest{State: read.State}, &deleted)
	if deleted.Diagnostics.HasError() {
		t.Fatal(deleted.Diagnostics)
	}
	if !api.envByName("staging").Archived {
		t.Fatal("created custom environment remains active after destroy")
	}
}

func TestWebhookMissingSigningKeyKeepsResource(t *testing.T) {
	ctx := context.Background()
	api := newFakeAPIState()
	api.webhooks = append(api.webhooks, &fakeWebhook{ID: "wh", Name: "original", Env: "production", Transform: stripeTransform})
	r := NewWebhookResource().(*webhookResource)
	r.data = &providerData{client: fakeClient(api)}
	var schema resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schema)
	model := webhookResourceModel{ID: types.StringValue("wh"), Name: types.StringValue("original"), Environment: types.StringValue("production"), Transform: types.StringValue(stripeTransform), EventFilter: types.ObjectNull(eventFilterAttrTypes)}
	state := tfsdk.State{Schema: schema.Schema}
	if diags := state.Set(ctx, model); diags.HasError() {
		t.Fatal(diags)
	}
	var deleted resource.DeleteResponse
	r.Delete(ctx, resource.DeleteRequest{State: state}, &deleted)
	if !deleted.Diagnostics.HasError() || !strings.Contains(deleted.Diagnostics[0].Detail(), "INNGEST_SIGNING_KEY") {
		t.Fatal(deleted.Diagnostics)
	}
	model.Name = types.StringValue("renamed")
	plan := tfsdk.Plan{Schema: schema.Schema}
	if diags := plan.Set(ctx, model); diags.HasError() {
		t.Fatal(diags)
	}
	var updated resource.UpdateResponse
	r.Update(ctx, resource.UpdateRequest{Plan: plan, State: state}, &updated)
	if !updated.Diagnostics.HasError() || !strings.Contains(updated.Diagnostics[0].Detail(), "inngest API keys cannot read signing keys") {
		t.Fatal(updated.Diagnostics)
	}
	if len(api.webhooks) != 1 || api.webhooks[0].Name != "original" {
		t.Fatal("webhook changed after a failed signing key lookup")
	}
	if api.calls["PUT /v1/webhooks/wh"] != 0 || api.calls["DELETE /v1/webhooks/wh"] != 0 {
		t.Fatal("v1 called without a signing key")
	}
}
