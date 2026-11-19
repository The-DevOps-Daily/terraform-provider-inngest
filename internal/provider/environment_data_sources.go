package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/datasourcevalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/The-DevOps-Daily/terraform-provider-inngest/internal/client"
)

var (
	_ datasource.DataSource                     = &environmentDataSource{}
	_ datasource.DataSourceWithConfigure        = &environmentDataSource{}
	_ datasource.DataSourceWithConfigValidators = &environmentDataSource{}
	_ datasource.DataSource                     = &environmentsDataSource{}
	_ datasource.DataSourceWithConfigure        = &environmentsDataSource{}
)

// NewEnvironmentDataSource returns the inngest_environment data source.
func NewEnvironmentDataSource() datasource.DataSource { return &environmentDataSource{} }

// NewEnvironmentsDataSource returns the inngest_environments data source.
func NewEnvironmentsDataSource() datasource.DataSource { return &environmentsDataSource{} }

type environmentDataSource struct{ data *providerData }

type environmentsDataSource struct{ data *providerData }

type environmentModel struct {
	ID        types.String `tfsdk:"id"`
	Name      types.String `tfsdk:"name"`
	Type      types.String `tfsdk:"type"`
	Archived  types.Bool   `tfsdk:"archived"`
	CreatedAt types.String `tfsdk:"created_at"`
}

type environmentsModel struct {
	Type            types.String       `tfsdk:"type"`
	IncludeArchived types.Bool         `tfsdk:"include_archived"`
	Environments    []environmentModel `tfsdk:"environments"`
}

func environmentFromAPI(e client.Env) environmentModel {
	return environmentModel{
		ID:        types.StringValue(e.ID),
		Name:      types.StringValue(e.Name),
		Type:      optionalString(e.Type),
		Archived:  types.BoolValue(e.IsArchived),
		CreatedAt: optionalString(e.CreatedAt),
	}
}

func (d *environmentDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_environment"
}

func (d *environmentDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Looks up one Inngest environment by ID or by name. Works for the production environment, custom environments and branch environments.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Environment ID. Set exactly one of `id` and `name`.",
				Optional:            true,
				Computed:            true,
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Environment name, such as `production`, `staging` or a branch name. Set exactly one of `id` and `name`. The lookup fails when several environments have this name.",
				Optional:            true,
				Computed:            true,
			},
			"type": schema.StringAttribute{
				MarkdownDescription: "Environment type: `PRODUCTION`, `TEST` or `BRANCH`.",
				Computed:            true,
			},
			"archived": schema.BoolAttribute{
				MarkdownDescription: "Whether the environment is archived.",
				Computed:            true,
			},
			"created_at": schema.StringAttribute{
				MarkdownDescription: "Creation time (RFC 3339).",
				Computed:            true,
			},
		},
	}
}

func (d *environmentDataSource) ConfigValidators(_ context.Context) []datasource.ConfigValidator {
	return []datasource.ConfigValidator{
		datasourcevalidator.ExactlyOneOf(path.MatchRoot("id"), path.MatchRoot("name")),
	}
}

func (d *environmentDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	data, diags := getProviderData(req.ProviderData)
	resp.Diagnostics.Append(diags...)
	if data != nil {
		d.data = data
	}
}

func (d *environmentDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg environmentModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	envs, err := d.data.client.ListEnvs(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Error listing Inngest environments", err.Error())
		return
	}
	var matches []client.Env
	for _, e := range envs {
		if (!cfg.ID.IsNull() && e.ID == cfg.ID.ValueString()) || (!cfg.Name.IsNull() && e.Name == cfg.Name.ValueString()) {
			matches = append(matches, e)
		}
	}
	switch len(matches) {
	case 0:
		resp.Diagnostics.AddError("Environment not found", fmt.Sprintf("No Inngest environment matches id=%s name=%s.", cfg.ID.String(), cfg.Name.String()))
		return
	case 1:
	default:
		resp.Diagnostics.AddError("Ambiguous environment name", fmt.Sprintf("%d environments are named %q. Look it up by id instead.", len(matches), cfg.Name.ValueString()))
		return
	}
	state := environmentFromAPI(matches[0])
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (d *environmentsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_environments"
}

func (d *environmentsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists the account's Inngest environments, optionally only one type. Use `type = \"BRANCH\"` to list branch environments.",
		Attributes: map[string]schema.Attribute{
			"type": schema.StringAttribute{
				MarkdownDescription: "Only list environments of this type: `PRODUCTION`, `TEST` or `BRANCH`.",
				Optional:            true,
				Validators:          []validator.String{stringvalidator.OneOf(client.EnvTypes...)},
			},
			"include_archived": schema.BoolAttribute{
				MarkdownDescription: "Also list archived environments. Defaults to `false`.",
				Optional:            true,
			},
			"environments": schema.ListNestedAttribute{
				MarkdownDescription: "The environments, in the order the API returns them.",
				Computed:            true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id":         schema.StringAttribute{Computed: true, MarkdownDescription: "Environment ID."},
						"name":       schema.StringAttribute{Computed: true, MarkdownDescription: "Environment name."},
						"type":       schema.StringAttribute{Computed: true, MarkdownDescription: "Environment type."},
						"archived":   schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether the environment is archived."},
						"created_at": schema.StringAttribute{Computed: true, MarkdownDescription: "Creation time (RFC 3339)."},
					},
				},
			},
		},
	}
}

func (d *environmentsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	data, diags := getProviderData(req.ProviderData)
	resp.Diagnostics.Append(diags...)
	if data != nil {
		d.data = data
	}
}

func (d *environmentsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg environmentsModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	envs, err := d.data.client.ListEnvs(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Error listing Inngest environments", err.Error())
		return
	}
	cfg.Environments = []environmentModel{}
	for _, e := range envs {
		if !cfg.Type.IsNull() && e.Type != cfg.Type.ValueString() {
			continue
		}
		if e.IsArchived && !cfg.IncludeArchived.ValueBool() {
			continue
		}
		cfg.Environments = append(cfg.Environments, environmentFromAPI(e))
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, cfg)...)
}
