package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// These tests run real terraform plan, apply, import and destroy cycles
// against fakeAPI. Each apply step is followed by a plan that must be empty,
// which catches perpetual diffs and "inconsistent result after apply".

func TestMockEnvironment(t *testing.T) {
	skipWithoutTerraform(t)
	api, url := newFakeAPI(t)
	api.pageSize = 1 // every list walks several pages
	p := fakeProviderConfig(url)
	var firstID string

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(*terraform.State) error {
			// The API cannot delete environments: destroy archives them.
			for _, e := range api.envs {
				if e.Name != "production" && !e.Archived {
					return fmt.Errorf("environment %s was not archived on destroy", e.Name)
				}
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: p + `resource "inngest_environment" "staging" { name = "staging" }`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("inngest_environment.staging", "id"),
					resource.TestCheckNoResourceAttr("inngest_environment.staging", "type"),
					resource.TestCheckResourceAttr("inngest_environment.staging", "archived", "false"),
					resource.TestCheckResourceAttrSet("inngest_environment.staging", "created_at"),
					func(s *terraform.State) error {
						firstID = s.RootModule().Resources["inngest_environment.staging"].Primary.ID
						return nil
					},
				),
			},
			{
				// Archive in place.
				Config: p + `resource "inngest_environment" "staging" {
  name     = "staging"
  archived = true
}`,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("inngest_environment.staging", plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("inngest_environment.staging", "archived", "true"),
					func(*terraform.State) error {
						if !api.envByName("staging").Archived {
							return fmt.Errorf("the API was not asked to archive")
						}
						return nil
					},
				),
			},
			{
				// Unarchive.
				Config: p + `resource "inngest_environment" "staging" {
  name     = "staging"
  archived = false
}`,
				Check: resource.TestCheckResourceAttr("inngest_environment.staging", "archived", "false"),
			},
			{
				// Without archived in the config, a change in the dashboard
				// (or branch auto-archive) is not undone.
				PreConfig: func() { api.envByName("staging").Archived = true },
				Config:    p + `resource "inngest_environment" "staging" { name = "staging" }`,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: resource.TestCheckResourceAttr("inngest_environment.staging", "archived", "true"),
			},
			{
				ResourceName:      "inngest_environment.staging",
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				// Import by name works too.
				ResourceName:      "inngest_environment.staging",
				ImportState:       true,
				ImportStateId:     "staging",
				ImportStateVerify: true,
			},
			{
				// An unknown name or ID is a clear error.
				ResourceName:  "inngest_environment.staging",
				ImportState:   true,
				ImportStateId: "nope",
				ExpectError:   regexp.MustCompile(`No Inngest environment has the ID or name "nope"`),
			},
			{
				Config: p + `resource "inngest_environment" "staging" {
  name     = "staging"
  archived = false
}`,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("inngest_environment.staging", plancheck.ResourceActionUpdate)},
				},
			},
			{
				// Deleted out of band: Terraform plans to create it again.
				PreConfig: func() { api.removeEnv(firstID) },
				Config:    p + `resource "inngest_environment" "staging" { name = "staging" }`,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("inngest_environment.staging", plancheck.ResourceActionCreate)},
				},
				Check: resource.TestCheckResourceAttr("inngest_environment.staging", "archived", "false"),
			},
			{
				// A new name replaces the environment and archives the old one.
				Config: p + `resource "inngest_environment" "staging" { name = "qa" }`,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("inngest_environment.staging", plancheck.ResourceActionDestroyBeforeCreate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("inngest_environment.staging", "name", "qa"),
					func(*terraform.State) error {
						if !api.envByName("staging").Archived {
							return fmt.Errorf("old environment was not archived")
						}
						return nil
					},
				),
			},
		},
	})
}

func TestMockEnvironmentCreateArchived(t *testing.T) {
	skipWithoutTerraform(t)
	api, url := newFakeAPI(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fakeProviderConfig(url) + `resource "inngest_environment" "old" {
  name     = "old"
  archived = true
}`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("inngest_environment.old", "archived", "true"),
					func(*terraform.State) error {
						if !api.envByName("old").Archived {
							return fmt.Errorf("not archived in the API")
						}
						return nil
					},
				),
			},
			{
				// If the list leaves archived environments out, an environment
				// archived in state stays in state instead of being planned again.
				PreConfig: func() { api.hideArchivedEnvs = true },
				Config: fakeProviderConfig(url) + `resource "inngest_environment" "old" {
  name     = "old"
  archived = true
}`,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
		},
	})
}

func TestMockEnvironmentConflict(t *testing.T) {
	skipWithoutTerraform(t)
	api, url := newFakeAPI(t)
	api.addEnv("staging", "TEST")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      fakeProviderConfig(url) + `resource "inngest_environment" "staging" { name = "staging" }`,
				ExpectError: regexp.MustCompile(`(?s)409.*Import it\s+with`),
			},
		},
	})
}

func TestMockEnvironmentRateLimited(t *testing.T) {
	skipWithoutTerraform(t)
	api, url := newFakeAPI(t)
	api.throttle["POST /v2/envs"] = 2
	api.throttle["GET /v2/envs"] = 1
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fakeProviderConfig(url) + `resource "inngest_environment" "staging" { name = "staging" }`,
				Check: func(*terraform.State) error {
					if api.calls["POST /v2/envs"] != 3 {
						return fmt.Errorf("POST /v2/envs called %d times, want 3", api.calls["POST /v2/envs"])
					}
					if n := len(api.envs); n != 2 {
						return fmt.Errorf("%d environments, want 2: a retried create must not duplicate", n)
					}
					return nil
				},
			},
		},
	})
}

// An imported production environment is never archived: destroy only drops
// it from state.
func TestMockEnvironmentProductionNotArchived(t *testing.T) {
	skipWithoutTerraform(t)
	api, url := newFakeAPI(t)
	prod := api.envByName("production")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(*terraform.State) error {
			if prod.Archived {
				return fmt.Errorf("production was archived")
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config:             fakeProviderConfig(url) + `resource "inngest_environment" "prod" { name = "production" }`,
				ResourceName:       "inngest_environment.prod",
				ImportState:        true,
				ImportStateId:      "production",
				ImportStatePersist: true,
			},
			{
				Config: fakeProviderConfig(url) + `resource "inngest_environment" "prod" { name = "production" }`,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: resource.TestCheckNoResourceAttr("inngest_environment.prod", "type"),
			},
		},
	})
}
