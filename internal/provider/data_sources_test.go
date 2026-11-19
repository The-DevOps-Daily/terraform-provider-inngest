package provider

import (
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestMockDataSources(t *testing.T) {
	skipWithoutTerraform(t)
	api, url := newFakeAPI(t)
	api.pageSize = 1
	staging := api.addEnv("staging", "TEST")
	staging.EventKeys = append(staging.EventKeys, "evtkey-second")
	api.addEnv("feature/login", "BRANCH")
	api.addEnv("old-branch", "BRANCH").Archived = true
	api.webhooks = append(api.webhooks, &fakeWebhook{ID: "wh-1", Env: "staging", Name: "GitHub", Transform: "function transform(e){return {name:'github/push',data:e}}", URL: "https://inn.gs/e/gh", CreatedAt: "2026-11-01T00:00:00Z", UpdatedAt: "2026-11-02T00:00:00Z"})
	api.apps["staging"] = []map[string]any{
		{"id": "billing", "name": "billing", "functionCount": 3, "method": "SERVE", "createdAt": "2026-10-01T00:00:00Z",
			"latestSync": map[string]any{"sdkLanguage": "typescript", "sdkVersion": "3.22.0", "status": "success", "url": "https://example.com/api/inngest", "framework": "nextjs"}},
		{"id": "legacy", "name": "legacy", "isArchived": true, "archivedAt": "2026-10-05T00:00:00Z"},
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fakeProviderConfig(url) + `
data "inngest_environments" "active" {}
data "inngest_environments" "branches" {
  type             = "BRANCH"
  include_archived = true
}
data "inngest_environment" "staging" { name = "staging" }
data "inngest_environment" "by_id" { id = data.inngest_environment.staging.id }
data "inngest_event_keys" "staging" { environment = "staging" }
data "inngest_event_keys" "named" {
  environment = "staging"
  name        = "Key 2"
}
data "inngest_signing_keys" "prod" {}
data "inngest_apps" "staging" { environment = "staging" }
data "inngest_apps" "archived" {
  environment = "staging"
  archived    = true
}
data "inngest_app" "billing" {
  environment = "staging"
  id          = "billing"
}
data "inngest_webhooks" "staging" { environment = "staging" }
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.inngest_environments.active", "environments.#", "3"),
					resource.TestCheckResourceAttr("data.inngest_environments.branches", "environments.#", "2"),
					resource.TestCheckResourceAttr("data.inngest_environments.branches", "environments.1.archived", "true"),
					resource.TestCheckResourceAttr("data.inngest_environment.staging", "type", "TEST"),
					resource.TestCheckResourceAttr("data.inngest_environment.staging", "archived", "false"),
					resource.TestCheckResourceAttrPair("data.inngest_environment.by_id", "name", "data.inngest_environment.staging", "name"),
					resource.TestCheckResourceAttr("data.inngest_event_keys.staging", "keys.#", "2"),
					resource.TestCheckResourceAttr("data.inngest_event_keys.staging", "keys.0.environment", "staging"),
					resource.TestCheckResourceAttr("data.inngest_event_keys.named", "keys.#", "1"),
					resource.TestCheckResourceAttr("data.inngest_event_keys.named", "keys.0.key", "evtkey-second"),
					// No environment: the API's default, production.
					resource.TestCheckResourceAttr("data.inngest_signing_keys.prod", "keys.#", "1"),
					resource.TestCheckResourceAttr("data.inngest_signing_keys.prod", "keys.0.environment", "production"),
					resource.TestMatchResourceAttr("data.inngest_signing_keys.prod", "keys.0.key", regexp.MustCompile(`^signkey-production-`)),
					resource.TestCheckResourceAttr("data.inngest_apps.staging", "apps.#", "1"),
					resource.TestCheckResourceAttr("data.inngest_apps.staging", "apps.0.id", "billing"),
					resource.TestCheckResourceAttr("data.inngest_apps.archived", "apps.0.id", "legacy"),
					resource.TestCheckResourceAttr("data.inngest_apps.archived", "apps.0.archived", "true"),
					resource.TestCheckNoResourceAttr("data.inngest_apps.archived", "apps.0.latest_sync"),
					resource.TestCheckResourceAttr("data.inngest_app.billing", "function_count", "3"),
					resource.TestCheckResourceAttr("data.inngest_app.billing", "method", "SERVE"),
					resource.TestCheckResourceAttr("data.inngest_app.billing", "latest_sync.status", "success"),
					resource.TestCheckResourceAttr("data.inngest_app.billing", "latest_sync.framework", "nextjs"),
					resource.TestCheckResourceAttr("data.inngest_webhooks.staging", "webhooks.#", "1"),
					resource.TestCheckResourceAttr("data.inngest_webhooks.staging", "webhooks.0.name", "GitHub"),
					resource.TestCheckResourceAttr("data.inngest_webhooks.staging", "webhooks.0.environment", "staging"),
					resource.TestCheckResourceAttr("data.inngest_webhooks.staging", "webhooks.0.url", "https://inn.gs/e/gh"),
				),
			},
		},
	})
}

func TestMockDataSourceErrors(t *testing.T) {
	skipWithoutTerraform(t)
	api, url := newFakeAPI(t)
	api.addEnv("dup", "TEST")
	api.addEnv("dup", "BRANCH")
	p := fakeProviderConfig(url)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      p + `data "inngest_environment" "x" { name = "missing" }`,
				ExpectError: regexp.MustCompile(`Environment not found`),
			},
			{
				Config:      p + `data "inngest_environment" "x" { name = "dup" }`,
				ExpectError: regexp.MustCompile(`2 environments are named "dup"`),
			},
			{
				Config:      p + `data "inngest_environment" "x" {}`,
				ExpectError: regexp.MustCompile(`Missing Attribute Configuration`),
			},
			{
				Config:      p + `data "inngest_app" "x" { id = "missing" }`,
				ExpectError: regexp.MustCompile(`GET /v2/apps/missing with 404`),
			},
			{
				Config:      p + `data "inngest_environments" "x" { type = "STAGING" }`,
				ExpectError: regexp.MustCompile(`value must be one of`),
			},
		},
	})
}
