package provider

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/The-DevOps-Daily/terraform-provider-inngest/internal/client"
)

// The TestAcc* tests call the real Inngest API. They run only with TF_ACC=1
// and INNGEST_API_KEY set, and fit the free plan:
//
//   - TestAccEnvironment creates one environment named tf-acc-<random> and
//     archives it at the end (the API cannot delete environments). The free
//     plan allows 3 active environments, so the account needs a free slot.
//   - TestAccWebhook creates, updates and deletes one webhook in
//     INNGEST_ACC_ENVIRONMENT (default production).
//   - TestAccDataSources only reads. Set INNGEST_ACC_APP_ID to an app synced
//     in that environment to also test inngest_app.

func accClient() *client.Client {
	c := client.New(os.Getenv(envAPIKey), os.Getenv(envBaseURL), "terraform-provider-inngest/acc-test")
	c.SigningKey = os.Getenv(envSigningKey)
	return c
}

func TestAccEnvironment(t *testing.T) {
	testAccPreCheck(t)
	name := accName()
	cfg := func(extra string) string {
		return fmt.Sprintf("resource \"inngest_environment\" \"test\" {\n  name = %q\n%s}\n", name, extra)
	}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(s *terraform.State) error {
			envs, err := accClient().ListEnvs(context.Background())
			if err != nil {
				return err
			}
			for _, e := range envs {
				if e.Name == name && !e.IsArchived {
					return fmt.Errorf("environment %s (%s) is still active after destroy", name, e.ID)
				}
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: cfg(""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("inngest_environment.test", "name", name),
					resource.TestCheckResourceAttrSet("inngest_environment.test", "id"),
					resource.TestCheckNoResourceAttr("inngest_environment.test", "type"),
					resource.TestCheckResourceAttr("inngest_environment.test", "archived", "false"),
				),
			},
			{
				Config: cfg("  archived = true\n"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("inngest_environment.test", plancheck.ResourceActionUpdate)},
				},
				Check: resource.TestCheckResourceAttr("inngest_environment.test", "archived", "true"),
			},
			{
				Config: cfg("  archived = false\n"),
				Check:  resource.TestCheckResourceAttr("inngest_environment.test", "archived", "false"),
			},
			{
				ResourceName:      "inngest_environment.test",
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				ResourceName:      "inngest_environment.test",
				ImportState:       true,
				ImportStateId:     name,
				ImportStateVerify: true,
			},
			{
				// Read back through the data sources.
				Config: cfg("") + fmt.Sprintf(`
data "inngest_environment" "test" {
  name       = %q
  depends_on = [inngest_environment.test]
}
`, name),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrPair("data.inngest_environment.test", "id", "inngest_environment.test", "id"),
				),
			},
		},
	})
}

func TestAccWebhook(t *testing.T) {
	testAccPreCheck(t)
	if os.Getenv(envSigningKey) == "" {
		t.Skip("INNGEST_SIGNING_KEY is required for real webhook updates and cleanup")
	}
	env := accEnvironment()
	name := accName()
	cfg := func(name, eventPrefix, extra string) string {
		return fmt.Sprintf(`resource "inngest_webhook" "test" {
  environment = %q
  name        = %q
  transform   = "function transform(evt, headers = {}, queryParams = {}) { return { name: '%s/' + (evt.type || 'received'), data: evt }; }"
%s}
`, env, name, eventPrefix, extra)
	}
	var firstURL string

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(s *terraform.State) error {
			hooks, err := accClient().ListWebhooks(context.Background(), env)
			if err != nil {
				return err
			}
			for _, h := range hooks {
				if h.Name == name || h.Name == name+"-renamed" {
					return fmt.Errorf("webhook %s (%s) still exists after destroy", h.Name, h.ID)
				}
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: cfg(name, "tfacc", ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("inngest_webhook.test", "id"),
					resource.TestCheckResourceAttrSet("inngest_webhook.test", "url"),
					resource.TestCheckResourceAttr("inngest_webhook.test", "environment", env),
					func(s *terraform.State) error {
						firstURL = s.RootModule().Resources["inngest_webhook.test"].Primary.Attributes["url"]
						return nil
					},
				),
			},
			{
				// In place through PUT /v1/webhooks/{id} with the signing key.
				Config: cfg(name+"-renamed", "tfacc2", ""),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("inngest_webhook.test", plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("inngest_webhook.test", "name", name+"-renamed"),
					func(s *terraform.State) error {
						if s.RootModule().Resources["inngest_webhook.test"].Primary.Attributes["url"] != firstURL {
							return fmt.Errorf("url changed on an in-place update")
						}
						return nil
					},
				),
			},
			{
				ResourceName:      "inngest_webhook.test",
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateIdFunc: func(s *terraform.State) (string, error) {
					return env + "/" + s.RootModule().Resources["inngest_webhook.test"].Primary.ID, nil
				},
			},
			{
				// Replace with an event filter and a response function.
				Config: cfg(name, "tfacc", `  event_filter = {
    filter = "ALLOW"
    events = ["tfacc/*"]
  }
  response = "function respond(body, headers) { return { status: 200, headers: {}, body: 'ok' }; }"
`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("inngest_webhook.test", plancheck.ResourceActionDestroyBeforeCreate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("inngest_webhook.test", "event_filter.filter", "ALLOW"),
					resource.TestCheckResourceAttr("inngest_webhook.test", "event_filter.events.#", "1"),
				),
			},
			{
				// Deleted out of band: Terraform notices and creates it again.
				PreConfig: func() {
					hooks, err := accClient().ListWebhooks(context.Background(), env)
					if err != nil {
						t.Fatal(err)
					}
					for _, h := range hooks {
						if h.Name == name {
							if err := accClient().DeleteWebhook(context.Background(), env, h.ID); err != nil {
								t.Fatal(err)
							}
						}
					}
				},
				Config: cfg(name, "tfacc", `  event_filter = {
    filter = "ALLOW"
    events = ["tfacc/*"]
  }
  response = "function respond(body, headers) { return { status: 200, headers: {}, body: 'ok' }; }"
`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("inngest_webhook.test", plancheck.ResourceActionCreate)},
				},
			},
		},
	})
}

func TestAccDataSources(t *testing.T) {
	testAccPreCheck(t)
	env := accEnvironment()
	config := fmt.Sprintf(`
data "inngest_environments" "all" {
  include_archived = true
}
data "inngest_environment" "env" { name = %[1]q }
data "inngest_apps" "env" { environment = %[1]q }
data "inngest_webhooks" "env" { environment = %[1]q }
`, env)
	checks := []resource.TestCheckFunc{
		resource.TestCheckResourceAttrSet("data.inngest_environments.all", "environments.0.id"),
		resource.TestCheckResourceAttrSet("data.inngest_environment.env", "id"),
		resource.TestCheckResourceAttrSet("data.inngest_apps.env", "apps.#"),
		resource.TestCheckResourceAttrSet("data.inngest_webhooks.env", "webhooks.#"),
	}
	if app := os.Getenv("INNGEST_ACC_APP_ID"); app != "" {
		config += fmt.Sprintf("data \"inngest_app\" \"app\" {\n  environment = %q\n  id          = %q\n}\n", env, app)
		checks = append(checks,
			resource.TestCheckResourceAttr("data.inngest_app.app", "id", app),
			resource.TestCheckResourceAttrSet("data.inngest_app.app", "name"),
			resource.TestCheckResourceAttrSet("data.inngest_app.app", "function_count"),
		)
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: config, Check: resource.ComposeAggregateTestCheckFunc(checks...)},
		},
	})
}

// Ordinary API keys cannot read either endpoint, even with all resource access.
func TestAccKeysDenied(t *testing.T) {
	testAccPreCheck(t)
	for _, kind := range []string{"event_keys", "signing_keys"} {
		t.Run(kind, func(t *testing.T) {
			resource.Test(t, resource.TestCase{
				ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
				Steps: []resource.TestStep{{
					Config:      fmt.Sprintf("data \"inngest_%s\" \"keys\" { environment = %q }", kind, accEnvironment()),
					ExpectError: regexp.MustCompile(`(?s)API\s+keys\s+cannot\s+read\s+keys.*credential\s+with\s+key\s+access`),
				}},
			})
		})
	}
}
