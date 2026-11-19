package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/The-DevOps-Daily/terraform-provider-inngest/internal/client"
)

var (
	_ datasource.DataSource              = &keysDataSource{}
	_ datasource.DataSourceWithConfigure = &keysDataSource{}
)

// NewEventKeysDataSource returns the inngest_event_keys data source.
func NewEventKeysDataSource() datasource.DataSource {
	return &keysDataSource{
		typeSuffix: "_event_keys",
		what:       "event keys",
		description: "Lists the event keys of an Inngest environment. An app sends events with an event key (`INNGEST_EVENT_KEY`). " +
			"All branch environments share the same event keys. The `key` values are sensitive.\n\n" +
			"The Inngest API cannot create or rotate event keys; do that in the dashboard.",
		list: func(ctx context.Context, c *client.Client, env string) ([]client.Key, error) {
			return c.ListEventKeys(ctx, env)
		},
	}
}

// NewSigningKeysDataSource returns the inngest_signing_keys data source.
func NewSigningKeysDataSource() datasource.DataSource {
	return &keysDataSource{
		typeSuffix: "_signing_keys",
		what:       "signing keys",
		description: "Lists the signing keys of an Inngest environment. A deployed app needs its environment's signing key (`INNGEST_SIGNING_KEY`) to talk to Inngest. " +
			"All branch environments share the same signing key. The `key` values are sensitive.\n\n" +
			"The Inngest API cannot create or rotate signing keys; rotate them in the dashboard. During a rotation the list can hold two keys.",
		list: func(ctx context.Context, c *client.Client, env string) ([]client.Key, error) {
			return c.ListSigningKeys(ctx, env)
		},
	}
}

type keysDataSource struct {
	data        *providerData
	typeSuffix  string
	what        string
	description string
	list        func(ctx context.Context, c *client.Client, env string) ([]client.Key, error)
}

type keyModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Environment types.String `tfsdk:"environment"`
	Key         types.String `tfsdk:"key"`
	CreatedAt   types.String `tfsdk:"created_at"`
}

type keysModel struct {
	Environment types.String `tfsdk:"environment"`
	Name        types.String `tfsdk:"name"`
	Keys        []keyModel   `tfsdk:"keys"`
}

func (d *keysDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + d.typeSuffix
}

func (d *keysDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: d.description,
		Attributes: map[string]schema.Attribute{
			"environment": schema.StringAttribute{
				MarkdownDescription: "Environment to list " + d.what + " for, sent as the `X-Inngest-Env` header. Defaults to the provider's `environment`; when neither is set, the API lists the production environment's keys.",
				Optional:            true,
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Only return keys with exactly this name.",
				Optional:            true,
			},
			"keys": schema.ListNestedAttribute{
				MarkdownDescription: "The " + d.what + ", in the order the API returns them.",
				Computed:            true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id":          schema.StringAttribute{Computed: true, MarkdownDescription: "Key ID."},
						"name":        schema.StringAttribute{Computed: true, MarkdownDescription: "Key name. Can be empty."},
						"environment": schema.StringAttribute{Computed: true, MarkdownDescription: "Environment the key belongs to."},
						"key":         schema.StringAttribute{Computed: true, Sensitive: true, MarkdownDescription: "The key value."},
						"created_at":  schema.StringAttribute{Computed: true, MarkdownDescription: "Creation time (RFC 3339)."},
					},
				},
			},
		},
	}
}

func (d *keysDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	data, diags := getProviderData(req.ProviderData)
	resp.Diagnostics.Append(diags...)
	if data != nil {
		d.data = data
	}
}

func (d *keysDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg keysModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	keys, err := d.list(ctx, d.data.client, d.data.headerEnv(cfg.Environment))
	if err != nil {
		resp.Diagnostics.AddError("Error listing Inngest "+d.what, err.Error())
		return
	}
	cfg.Keys = []keyModel{}
	for _, k := range keys {
		if !cfg.Name.IsNull() && k.Name != cfg.Name.ValueString() {
			continue
		}
		cfg.Keys = append(cfg.Keys, keyModel{
			ID:          types.StringValue(k.ID),
			Name:        types.StringValue(k.Name),
			Environment: types.StringValue(k.Environment),
			Key:         types.StringValue(k.Key),
			CreatedAt:   optionalString(k.CreatedAt),
		})
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, cfg)...)
}
