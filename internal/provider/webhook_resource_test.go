package provider

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const stripeTransform = `function transform(evt, headers = {}, queryParams = {}) {
  return { id: evt.id, name: "stripe/" + evt.type, data: evt };
}`

func webhookConfig(name, transform, extra string) string {
	return fmt.Sprintf(`resource "inngest_webhook" "stripe" {
  name      = %q
  transform = <<-EOT
%s
EOT
%s
}`, name, transform, extra)
}

// webhookURL reads the generated URL from state.
func webhookURL(s *terraform.State) string {
	return s.RootModule().Resources["inngest_webhook.stripe"].Primary.Attributes["url"]
}

func TestMockWebhook(t *testing.T) {
	skipWithoutTerraform(t)
	api, url := newFakeAPI(t)
	api.pageSize = 1
	// Another webhook in the environment, so lookups have to page.
	api.webhooks = append(api.webhooks, &fakeWebhook{ID: "other", Env: "production", Name: "Other", Transform: "function transform(e){return {name:'x',data:e}}", URL: "https://inn.gs/e/other"})
	p := fakeKeyAccessProviderConfig(url)
	var firstURL, firstID string

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(*terraform.State) error {
			for _, w := range api.webhooks {
				if w.ID != "other" {
					return fmt.Errorf("webhook %s still exists after destroy", w.ID)
				}
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: p + webhookConfig("Stripe", stripeTransform, ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("inngest_webhook.stripe", "id"),
					// No environment anywhere: production.
					resource.TestCheckResourceAttr("inngest_webhook.stripe", "environment", "production"),
					resource.TestCheckResourceAttr("inngest_webhook.stripe", "name", "Stripe"),
					resource.TestMatchResourceAttr("inngest_webhook.stripe", "url", regexp.MustCompile(`^https://inn\.gs/e/`)),
					resource.TestCheckNoResourceAttr("inngest_webhook.stripe", "response"),
					resource.TestCheckNoResourceAttr("inngest_webhook.stripe", "event_filter"),
					resource.TestCheckResourceAttrSet("inngest_webhook.stripe", "created_at"),
					resource.TestCheckResourceAttrSet("inngest_webhook.stripe", "updated_at"),
					func(s *terraform.State) error {
						firstURL = webhookURL(s)
						firstID = s.RootModule().Resources["inngest_webhook.stripe"].Primary.ID
						return nil
					},
				),
			},
			{
				// Renaming and a new transform update in place through v1,
				// with the signing key; the URL stays.
				Config: p + webhookConfig("Stripe payments", strings.ReplaceAll(stripeTransform, "stripe/", "stripe/v2/"), ""),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("inngest_webhook.stripe", plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("inngest_webhook.stripe", "name", "Stripe payments"),
					resource.TestCheckResourceAttrWith("inngest_webhook.stripe", "transform", func(v string) error {
						if !strings.Contains(v, "stripe/v2/") {
							return fmt.Errorf("transform not updated: %s", v)
						}
						return nil
					}),
					func(s *terraform.State) error {
						if webhookURL(s) != firstURL {
							return fmt.Errorf("url changed on an in-place update")
						}
						if api.calls["PUT /v1/webhooks/"+firstID] != 1 {
							return fmt.Errorf("PUT /v1/webhooks called %d times", api.calls["PUT /v1/webhooks/"+firstID])
						}
						return nil
					},
				),
			},
			{
				// The API keeping the transform with other surrounding
				// whitespace is not drift.
				PreConfig: func() {
					wh := api.webhookByID(firstID)
					wh.Transform = "\n\n" + strings.TrimSpace(wh.Transform) + "   \n"
				},
				Config:   p + webhookConfig("Stripe payments", strings.ReplaceAll(stripeTransform, "stripe/", "stripe/v2/"), ""),
				PlanOnly: true,
			},
			{
				// A rename in the dashboard is drift, and the apply puts it back.
				PreConfig: func() { api.webhookByID(firstID).Name = "renamed in dashboard" },
				Config:    p + webhookConfig("Stripe payments", strings.ReplaceAll(stripeTransform, "stripe/", "stripe/v2/"), ""),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("inngest_webhook.stripe", plancheck.ResourceActionUpdate)},
				},
				Check: resource.TestCheckResourceAttr("inngest_webhook.stripe", "name", "Stripe payments"),
			},
			{
				ResourceName:      "inngest_webhook.stripe",
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateIdFunc: func(s *terraform.State) (string, error) {
					return "production/" + s.RootModule().Resources["inngest_webhook.stripe"].Primary.ID, nil
				},
			},
			{
				// A bare ID imports into the default environment.
				ResourceName:      "inngest_webhook.stripe",
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				// An event filter cannot change in place: replace.
				Config: p + webhookConfig("Stripe payments", stripeTransform, `  event_filter = {
    filter = "ALLOW"
    events = ["stripe/invoice.*", "stripe/charge.succeeded"]
  }
  response = "function respond(body, headers) { return { status: 200, headers: {}, body: '' } }"`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("inngest_webhook.stripe", plancheck.ResourceActionDestroyBeforeCreate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					// ALLOW is the enum default, so the API leaves it out of
					// the response; it must still read back as ALLOW.
					resource.TestCheckResourceAttr("inngest_webhook.stripe", "event_filter.filter", "ALLOW"),
					resource.TestCheckResourceAttr("inngest_webhook.stripe", "event_filter.events.#", "2"),
					resource.TestCheckResourceAttrSet("inngest_webhook.stripe", "response"),
					func(s *terraform.State) error {
						if webhookURL(s) == firstURL {
							return fmt.Errorf("a replaced webhook should have a new url")
						}
						if api.webhookByID(firstID) != nil {
							return fmt.Errorf("the old webhook was not deleted")
						}
						return nil
					},
				),
			},
			{
				Config: p + webhookConfig("Stripe payments", stripeTransform, `  event_filter = {
    filter = "DENY"
    events = ["stripe/customer.*"]
  }`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("inngest_webhook.stripe", plancheck.ResourceActionDestroyBeforeCreate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("inngest_webhook.stripe", "event_filter.filter", "DENY"),
					resource.TestCheckNoResourceAttr("inngest_webhook.stripe", "response"),
				),
			},
			{
				// Deleted out of band: removed from state and created again.
				PreConfig: func() {
					for _, w := range append([]*fakeWebhook(nil), api.webhooks...) {
						if w.ID != "other" {
							api.removeWebhook(w.ID)
						}
					}
				},
				Config: p + webhookConfig("Stripe payments", stripeTransform, `  event_filter = {
    filter = "DENY"
    events = ["stripe/customer.*"]
  }`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("inngest_webhook.stripe", plancheck.ResourceActionCreate)},
				},
			},
		},
	})
}

// The environment comes from the resource, else the provider; moving between
// them replaces the webhook, which then lives in the other environment.
func TestMockWebhookEnvironments(t *testing.T) {
	skipWithoutTerraform(t)
	api, url := newFakeAPI(t)
	api.addEnv("staging", "TEST")
	api.addEnv("feature/login", "BRANCH")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fakeKeyAccessProviderConfigEnv(url, "staging") + webhookConfig("Stripe", stripeTransform, ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("inngest_webhook.stripe", "environment", "staging"),
					func(*terraform.State) error {
						if len(api.webhooks) != 1 || api.webhooks[0].Env != "staging" {
							return fmt.Errorf("webhook not created in staging: %+v", api.webhooks)
						}
						return nil
					},
				),
			},
			{
				// An in-place update in staging uses staging's signing key.
				Config: fakeKeyAccessProviderConfigEnv(url, "staging") + webhookConfig("Stripe staging", stripeTransform, ""),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("inngest_webhook.stripe", plancheck.ResourceActionUpdate)},
				},
			},
			{
				// The resource's own environment wins over the provider's.
				Config: fakeKeyAccessProviderConfigEnv(url, "staging") + webhookConfig("Stripe", stripeTransform, `  environment = "feature/login"`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("inngest_webhook.stripe", plancheck.ResourceActionDestroyBeforeCreate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("inngest_webhook.stripe", "environment", "feature/login"),
					func(*terraform.State) error {
						if len(api.webhooks) != 1 || api.webhooks[0].Env != "feature/login" {
							return fmt.Errorf("webhook not moved: %+v", api.webhooks)
						}
						return nil
					},
				),
			},
			{
				// Branch names contain slashes; the import ID splits at the last one.
				ResourceName:      "inngest_webhook.stripe",
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateIdFunc: func(s *terraform.State) (string, error) {
					return "feature/login/" + s.RootModule().Resources["inngest_webhook.stripe"].Primary.ID, nil
				},
			},
			{
				// A change of the provider default replaces webhooks that use it.
				Config: fakeKeyAccessProviderConfigEnv(url, "staging") + webhookConfig("Stripe", stripeTransform, ""),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("inngest_webhook.stripe", plancheck.ResourceActionDestroyBeforeCreate)},
				},
			},
			{
				Config: fakeKeyAccessProviderConfigEnv(url, "production") + webhookConfig("Stripe", stripeTransform, ""),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("inngest_webhook.stripe", plancheck.ResourceActionDestroyBeforeCreate)},
				},
				Check: resource.TestCheckResourceAttr("inngest_webhook.stripe", "environment", "production"),
			},
		},
	})
}

func TestMockWebhookErrors(t *testing.T) {
	skipWithoutTerraform(t)
	_, url := newFakeAPI(t)
	p := fakeKeyAccessProviderConfig(url)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      p + webhookConfig(strings.Repeat("n", 129), stripeTransform, ""),
				ExpectError: regexp.MustCompile(`string length must be between 1 and 128`),
			},
			{
				Config: p + webhookConfig("Stripe", stripeTransform, `  event_filter = {
    filter = "BLOCK"
    events = ["a"]
  }`),
				ExpectError: regexp.MustCompile(`value must be one of`),
			},
			{
				Config: p + `resource "inngest_webhook" "stripe" {
  name      = "Stripe"
  transform = "   "
}`,
				ExpectError: regexp.MustCompile(`Empty transform`),
			},
			{
				// The API's error body reaches the user.
				Config:      p + webhookConfig("Stripe", stripeTransform, `  environment = "does-not-exist"`),
				ExpectError: regexp.MustCompile(`POST /v2/env/webhooks with 404: not_found:\s+Environment not found`),
			},
		},
	})
}

func TestMockWebhookRateLimited(t *testing.T) {
	skipWithoutTerraform(t)
	api, url := newFakeAPI(t)
	api.throttle["POST /v2/env/webhooks"] = 1
	api.throttle["GET /v2/env/webhooks"] = 2
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fakeKeyAccessProviderConfig(url) + webhookConfig("Stripe", stripeTransform, ""),
				Check: func(*terraform.State) error {
					if len(api.webhooks) != 1 {
						return fmt.Errorf("%d webhooks after a retried create", len(api.webhooks))
					}
					return nil
				},
			},
		},
	})
}

// An environment known only at apply time may differ from the current one, so
// the plan must already be a replacement; deciding at apply time would make
// Terraform refuse the changed action.
func TestMockWebhookUnknownEnvironment(t *testing.T) {
	skipWithoutTerraform(t)
	api, url := newFakeAPI(t)
	api.addEnv("staging", "TEST")
	cfg := func(env string) string {
		return fakeKeyAccessProviderConfig(url) + fmt.Sprintf(`resource "terraform_data" "env" { input = %q }
`, env) + webhookConfig("Stripe", stripeTransform, `  environment = terraform_data.env.output`)
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: cfg("production"),
				Check:  resource.TestCheckResourceAttr("inngest_webhook.stripe", "environment", "production"),
			},
			{
				Config: cfg("staging"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("inngest_webhook.stripe", plancheck.ResourceActionDestroyBeforeCreate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("inngest_webhook.stripe", "environment", "staging"),
					func(*terraform.State) error {
						if len(api.webhooks) != 1 || api.webhooks[0].Env != "staging" {
							return fmt.Errorf("webhooks = %+v", api.webhooks)
						}
						return nil
					},
				),
			},
		},
	})
}

// A failed prerequisite is an error, never a reason to drop state: a 404 from
// the account-level lists must not read as "the resource is gone".
func TestMockAccountLevel404KeepsState(t *testing.T) {
	skipWithoutTerraform(t)
	api, url := newFakeAPI(t)
	p := fakeKeyAccessProviderConfig(url)
	both := p + `resource "inngest_environment" "staging" { name = "staging" }
` + webhookConfig("Stripe", stripeTransform, "")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: both},
			{
				// The environment refresh fails instead of planning a create.
				PreConfig:   func() { api.envsAccountMissing = true },
				Config:      both,
				ExpectError: regexp.MustCompile(`Account not found`),
			},
			{
				// Deleting the webhook needs the signing key; the failed
				// lookup must fail the destroy and keep the webhook.
				PreConfig: func() {
					api.envsAccountMissing = false
					api.keysAccountMissing = true
				},
				Config:      p + `resource "inngest_environment" "staging" { name = "staging" }`,
				ExpectError: regexp.MustCompile(`(?s)signing key.*Account not found`),
				Check: func(*terraform.State) error {
					if len(api.webhooks) != 1 {
						return fmt.Errorf("webhook gone: %+v", api.webhooks)
					}
					return nil
				},
			},
			{
				PreConfig: func() { api.keysAccountMissing = false },
				Config:    both,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
		},
	})
}
