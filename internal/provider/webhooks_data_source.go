package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ datasource.DataSource              = &webhooksDataSource{}
	_ datasource.DataSourceWithConfigure = &webhooksDataSource{}
)

// NewWebhooksDataSource returns the inngest_webhooks data source.
func NewWebhooksDataSource() datasource.DataSource { return &webhooksDataSource{} }

type webhooksDataSource struct{ data *providerData }

type webhookItemModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Environment types.String `tfsdk:"environment"`
	URL         types.String `tfsdk:"url"`
	Transform   types.String `tfsdk:"transform"`
	Response    types.String `tfsdk:"response"`
	EventFilter types.Object `tfsdk:"event_filter"`
	CreatedAt   types.String `tfsdk:"created_at"`
	UpdatedAt   types.String `tfsdk:"updated_at"`
}

type webhooksModel struct {
	Environment types.String       `tfsdk:"environment"`
	Webhooks    []webhookItemModel `tfsdk:"webhooks"`
}

func (d *webhooksDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_webhooks"
}

func (d *webhooksDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists the webhooks of an Inngest environment, including ones created in the dashboard. The webhook URLs are sensitive.",
		Attributes: map[string]schema.Attribute{
			"environment": schema.StringAttribute{
				MarkdownDescription: "Environment to list webhooks for, sent as the `X-Inngest-Env` header. Defaults to the provider's `environment`, then to `production`.",
				Optional:            true,
			},
			"webhooks": schema.ListNestedAttribute{
				MarkdownDescription: "The webhooks, in the order the API returns them.",
				Computed:            true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id":          schema.StringAttribute{Computed: true, MarkdownDescription: "Webhook ID."},
						"name":        schema.StringAttribute{Computed: true, MarkdownDescription: "Webhook name."},
						"environment": schema.StringAttribute{Computed: true, MarkdownDescription: "Environment the webhook belongs to."},
						"url":         schema.StringAttribute{Computed: true, Sensitive: true, MarkdownDescription: "The webhook URL."},
						"transform":   schema.StringAttribute{Computed: true, MarkdownDescription: "JavaScript transform function."},
						"response":    schema.StringAttribute{Computed: true, MarkdownDescription: "JavaScript function that answers GET requests, if set."},
						"event_filter": schema.SingleNestedAttribute{
							Computed:            true,
							MarkdownDescription: "Event filter, if set.",
							Attributes: map[string]schema.Attribute{
								"filter": schema.StringAttribute{Computed: true, MarkdownDescription: "`ALLOW` or `DENY`."},
								"events": schema.SetAttribute{Computed: true, ElementType: types.StringType, MarkdownDescription: "Event names or patterns."},
							},
						},
						"created_at": schema.StringAttribute{Computed: true, MarkdownDescription: "Creation time (RFC 3339)."},
						"updated_at": schema.StringAttribute{Computed: true, MarkdownDescription: "Last update time (RFC 3339)."},
					},
				},
			},
		},
	}
}

func (d *webhooksDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	data, diags := getProviderData(req.ProviderData)
	resp.Diagnostics.Append(diags...)
	if data != nil {
		d.data = data
	}
}

func (d *webhooksDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg webhooksModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	env := d.data.resourceEnv(cfg.Environment)
	hooks, err := d.data.client.ListWebhooks(ctx, env)
	if err != nil {
		resp.Diagnostics.AddError("Error listing Inngest webhooks", err.Error())
		return
	}
	cfg.Webhooks = []webhookItemModel{}
	for i := range hooks {
		m := webhookResourceModel{Environment: types.StringValue(env), Transform: types.StringNull(), Response: types.StringNull()}
		resp.Diagnostics.Append(mapWebhook(ctx, &hooks[i], &m)...)
		if resp.Diagnostics.HasError() {
			return
		}
		envName := m.Environment
		if hooks[i].Environment != "" {
			envName = types.StringValue(hooks[i].Environment)
		}
		cfg.Webhooks = append(cfg.Webhooks, webhookItemModel{
			ID:          m.ID,
			Name:        m.Name,
			Environment: envName,
			URL:         m.URL,
			Transform:   m.Transform,
			Response:    m.Response,
			EventFilter: m.EventFilter,
			CreatedAt:   m.CreatedAt,
			UpdatedAt:   m.UpdatedAt,
		})
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, cfg)...)
}
