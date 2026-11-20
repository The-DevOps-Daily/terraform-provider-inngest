// Package provider implements the Inngest Terraform provider.
package provider

import (
	"context"
	"errors"
	"os"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/The-DevOps-Daily/terraform-provider-inngest/internal/client"
)

// Environment variables the provider reads.
const (
	envSigningKey  = "INNGEST_SIGNING_KEY"
	envAPIKey      = "INNGEST_API_KEY"
	envBaseURL     = "INNGEST_API_BASE_URL"
	envEnvironment = "INNGEST_ENV"
)

// defaultEnvironment is the X-Inngest-Env value used when neither the
// resource nor the provider names an environment and the endpoint needs one.
const defaultEnvironment = "production"

var _ provider.Provider = &inngestProvider{}

type inngestProvider struct {
	version string
}

// New returns a provider factory for the given build version.
func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &inngestProvider{version: version}
	}
}

// providerData is what resources and data sources get from Configure.
type providerData struct {
	client *client.Client
	// environment is the provider-level environment, or "" when unset.
	environment string
}

type inngestProviderModel struct {
	SigningKey  types.String `tfsdk:"signing_key"`
	APIKey      types.String `tfsdk:"api_key"`
	BaseURL     types.String `tfsdk:"base_url"`
	Environment types.String `tfsdk:"environment"`
}

func (p *inngestProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "inngest"
	resp.Version = p.version
}

func (p *inngestProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "The Inngest provider manages [Inngest](https://www.inngest.com) environments and webhooks through the " +
			"[Inngest REST API](https://api-docs.inngest.com), and reads environments, apps, event keys and signing keys.\n\n" +
			"This is a community provider. It is not affiliated with or endorsed by Inngest, Inc.\n\n" +
			"Authenticate with an Inngest API key (create one on the [API keys page](https://app.inngest.com/settings/api-keys)). " +
			"Most calls act on one environment, which the API selects with the `X-Inngest-Env` header. Set `environment` here for a default, " +
			"and override it on each resource or data source.\n\n" +
			"API keys cannot read event or signing keys. Key data sources require a credential with key access. For webhook updates and deletes, supply the environment signing key from the dashboard through `signing_key` or `INNGEST_SIGNING_KEY`.",
		Attributes: map[string]schema.Attribute{
			"api_key": schema.StringAttribute{
				MarkdownDescription: "Inngest API key (it starts with `sk-inn-api-`). Can also be set with the `INNGEST_API_KEY` environment variable. " +
					"It is checked when a resource or data source calls the API, so a configuration that never calls the API plans without one.",
				Optional:  true,
				Sensitive: true,
			},
			"signing_key": schema.StringAttribute{
				MarkdownDescription: "Environment signing key from the Inngest dashboard, used for webhook updates and deletes. Can also be set with `INNGEST_SIGNING_KEY`. A webhook's `signing_key` overrides this value. API keys cannot read signing keys.",
				Optional:            true,
				Sensitive:           true,
			},
			"base_url": schema.StringAttribute{
				MarkdownDescription: "Base URL of the Inngest REST API, without the `/v1` or `/v2` suffix. Defaults to `https://api.inngest.com`. " +
					"Can also be set with the `INNGEST_API_BASE_URL` environment variable.",
				Optional: true,
			},
			"environment": schema.StringAttribute{
				MarkdownDescription: "Default environment for resources and data sources that do not set their own, sent as the `X-Inngest-Env` header. " +
					"Use the environment name, for example `production`, a custom environment such as `staging`, or a branch name such as `feature/my-branch`. " +
					"Can also be set with the `INNGEST_ENV` environment variable (the same variable the Inngest SDKs read). " +
					"When unset, all environment-scoped calls send `production`.",
				Optional: true,
			},
		},
	}
}

func (p *inngestProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var config inngestProviderModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var keyErr error
	apiKey := os.Getenv(envAPIKey)
	switch {
	case config.APIKey.IsUnknown():
		apiKey = ""
		keyErr = errors.New("the Inngest api_key is unknown when this call is made. Set it to a value known at plan time, or use the INNGEST_API_KEY environment variable")
	case !config.APIKey.IsNull():
		apiKey = config.APIKey.ValueString()
	}
	if keyErr == nil && apiKey == "" {
		keyErr = errors.New("missing Inngest API key. Set the api_key argument of the inngest provider or the INNGEST_API_KEY environment variable")
	}

	if config.BaseURL.IsUnknown() || config.Environment.IsUnknown() {
		resp.Diagnostics.AddAttributeError(path.Root("base_url"), "Unknown provider configuration",
			"The inngest provider's base_url and environment must be known at plan time.")
		return
	}
	baseURL := os.Getenv(envBaseURL)
	if !config.BaseURL.IsNull() {
		baseURL = config.BaseURL.ValueString()
	}
	environment := os.Getenv(envEnvironment)
	if !config.Environment.IsNull() {
		environment = config.Environment.ValueString()
	}

	if config.SigningKey.IsUnknown() {
		resp.Diagnostics.AddAttributeError(path.Root("signing_key"), "Unknown signing key",
			"The provider signing_key must be known at plan time. Set it directly, use INNGEST_SIGNING_KEY, or set signing_key on the webhook.")
		return
	}
	signingKey := os.Getenv(envSigningKey)
	if !config.SigningKey.IsNull() {
		signingKey = config.SigningKey.ValueString()
	}

	c := client.New(apiKey, baseURL, "terraform-provider-inngest/"+p.version)
	c.KeyError = keyErr
	c.SigningKey = signingKey
	data := &providerData{client: c, environment: environment}
	resp.ResourceData = data
	resp.DataSourceData = data
}

func (p *inngestProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewEnvironmentResource,
		NewWebhookResource,
	}
}

func (p *inngestProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewEnvironmentDataSource,
		NewEnvironmentsDataSource,
		NewEventKeysDataSource,
		NewSigningKeysDataSource,
		NewAppDataSource,
		NewAppsDataSource,
		NewWebhooksDataSource,
	}
}
