package provider

import (
	"fmt"
	"os"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// testAccProtoV6ProviderFactories wires the provider into the test framework.
var testAccProtoV6ProviderFactories = map[string]func() (tfprotov6.ProviderServer, error){
	"inngest": providerserver.NewProtocol6WithError(New("test")()),
}

// testAccPreCheck skips a real-API test unless INNGEST_API_KEY is set. The
// framework itself skips these tests unless TF_ACC=1.
func testAccPreCheck(t *testing.T) {
	t.Helper()
	if os.Getenv(envAPIKey) == "" {
		t.Skip("INNGEST_API_KEY is not set; skipping the real-API acceptance test")
	}
}

// accName returns a random name with the tf-acc prefix, so leftovers are easy
// to find and clean up.
func accName() string {
	return "tf-acc-" + acctest.RandString(8)
}

// accEnvironment is the environment the real-API webhook tests use.
func accEnvironment() string {
	if v := os.Getenv("INNGEST_ACC_ENVIRONMENT"); v != "" {
		return v
	}
	return "production"
}

func TestProviderSchemaIsValid(t *testing.T) {
	// Building the provider server validates every schema.
	if _, err := testAccProtoV6ProviderFactories["inngest"](); err != nil {
		t.Fatal(err)
	}
}

// A configuration that never calls the API plans and applies without a key.
func TestNoKeyNeededWithoutAPICalls(t *testing.T) {
	skipWithoutTerraform(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `provider "inngest" {}
resource "inngest_environment" "unused" {
  count = 0
  name  = "unused"
}`,
			},
		},
	})
}

func TestMissingKeyError(t *testing.T) {
	skipWithoutTerraform(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      `provider "inngest" {}` + "\n" + `data "inngest_environments" "all" {}`,
				ExpectError: regexp.MustCompile(`missing Inngest API key`),
			},
		},
	})
}

func TestWrongKeyShowsAPIError(t *testing.T) {
	skipWithoutTerraform(t)
	_, url := newFakeAPI(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      fmt.Sprintf("provider \"inngest\" {\n  api_key  = \"sk-inn-api-wrong\"\n  base_url = %q\n}\n", url) + `data "inngest_environments" "all" {}`,
				ExpectError: regexp.MustCompile(`GET /v2/envs with 401: Authentication failed`),
			},
		},
	})
}

// The provider reads INNGEST_API_KEY, INNGEST_API_BASE_URL and INNGEST_ENV.
func TestProviderEnvironmentVariables(t *testing.T) {
	skipWithoutTerraform(t)
	api, url := newFakeAPI(t)
	api.addEnv("staging", "TEST")
	t.Setenv(envAPIKey, fakeAPIKey)
	t.Setenv(envBaseURL, url)
	t.Setenv(envEnvironment, "staging")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `data "inngest_signing_keys" "k" {}`,
				Check:  resource.TestCheckResourceAttr("data.inngest_signing_keys.k", "keys.0.environment", "staging"),
			},
		},
	})
}
