package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/The-DevOps-Daily/terraform-provider-inngest/internal/client"
)

var (
	_ datasource.DataSource              = &appDataSource{}
	_ datasource.DataSourceWithConfigure = &appDataSource{}
	_ datasource.DataSource              = &appsDataSource{}
	_ datasource.DataSourceWithConfigure = &appsDataSource{}
)

// NewAppDataSource returns the inngest_app data source.
func NewAppDataSource() datasource.DataSource { return &appDataSource{} }

// NewAppsDataSource returns the inngest_apps data source.
func NewAppsDataSource() datasource.DataSource { return &appsDataSource{} }

type appDataSource struct{ data *providerData }

type appsDataSource struct{ data *providerData }

type appModel struct {
	Environment   types.String `tfsdk:"environment"`
	ID            types.String `tfsdk:"id"`
	Name          types.String `tfsdk:"name"`
	AppVersion    types.String `tfsdk:"app_version"`
	Archived      types.Bool   `tfsdk:"archived"`
	ArchivedAt    types.String `tfsdk:"archived_at"`
	CreatedAt     types.String `tfsdk:"created_at"`
	FunctionCount types.Int64  `tfsdk:"function_count"`
	Method        types.String `tfsdk:"method"`
	LatestSync    types.Object `tfsdk:"latest_sync"`
}

type appItemModel struct {
	ID            types.String `tfsdk:"id"`
	Name          types.String `tfsdk:"name"`
	AppVersion    types.String `tfsdk:"app_version"`
	Archived      types.Bool   `tfsdk:"archived"`
	ArchivedAt    types.String `tfsdk:"archived_at"`
	CreatedAt     types.String `tfsdk:"created_at"`
	FunctionCount types.Int64  `tfsdk:"function_count"`
	Method        types.String `tfsdk:"method"`
	LatestSync    types.Object `tfsdk:"latest_sync"`
}

type appsModel struct {
	Environment types.String   `tfsdk:"environment"`
	Archived    types.Bool     `tfsdk:"archived"`
	Apps        []appItemModel `tfsdk:"apps"`
}

var appSyncAttrTypes = map[string]attr.Type{
	"app_version":  types.StringType,
	"error":        types.StringType,
	"framework":    types.StringType,
	"sdk_language": types.StringType,
	"sdk_version":  types.StringType,
	"status":       types.StringType,
	"synced_at":    types.StringType,
	"url":          types.StringType,
}

// appComputedAttributes are the app fields both data sources report.
func appComputedAttributes() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"name":           schema.StringAttribute{Computed: true, MarkdownDescription: "App name."},
		"app_version":    schema.StringAttribute{Computed: true, MarkdownDescription: "User-defined app version, if set."},
		"archived":       schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether the app is archived."},
		"archived_at":    schema.StringAttribute{Computed: true, MarkdownDescription: "When the app was archived (RFC 3339), if it is."},
		"created_at":     schema.StringAttribute{Computed: true, MarkdownDescription: "When the app was first synced (RFC 3339)."},
		"function_count": schema.Int64Attribute{Computed: true, MarkdownDescription: "Number of functions in the app."},
		"method":         schema.StringAttribute{Computed: true, MarkdownDescription: "How the app connects: `UNSPECIFIED`, `SERVE`, `CONNECT`, `API` or `INNGEST_SERVE`."},
		"latest_sync": schema.SingleNestedAttribute{
			Computed:            true,
			MarkdownDescription: "The latest sync of the app, if any.",
			Attributes: map[string]schema.Attribute{
				"app_version":  schema.StringAttribute{Computed: true, MarkdownDescription: "App version reported by the sync."},
				"error":        schema.StringAttribute{Computed: true, MarkdownDescription: "Most recent sync or connection error, if any."},
				"framework":    schema.StringAttribute{Computed: true, MarkdownDescription: "Framework reported by the SDK, such as `nextjs`."},
				"sdk_language": schema.StringAttribute{Computed: true, MarkdownDescription: "Language of the SDK, such as `typescript`."},
				"sdk_version":  schema.StringAttribute{Computed: true, MarkdownDescription: "Version of the SDK."},
				"status":       schema.StringAttribute{Computed: true, MarkdownDescription: "Sync status: `pending`, `success`, `error` or `duplicate`."},
				"synced_at":    schema.StringAttribute{Computed: true, MarkdownDescription: "Time of the sync (RFC 3339)."},
				"url":          schema.StringAttribute{Computed: true, MarkdownDescription: "URL of the app's Inngest endpoint, if synced by URL."},
			},
		},
	}
}

func appSyncValue(s *client.AppSync) (types.Object, error) {
	if s == nil {
		return types.ObjectNull(appSyncAttrTypes), nil
	}
	obj, diags := types.ObjectValue(appSyncAttrTypes, map[string]attr.Value{
		"app_version":  optionalString(s.AppVersion),
		"error":        optionalString(s.Error),
		"framework":    optionalString(s.Framework),
		"sdk_language": optionalString(s.SDKLanguage),
		"sdk_version":  optionalString(s.SDKVersion),
		"status":       optionalString(s.Status),
		"synced_at":    optionalString(s.SyncedAt),
		"url":          optionalString(s.URL),
	})
	if diags.HasError() {
		return obj, fmt.Errorf("building latest_sync: %v", diags)
	}
	return obj, nil
}

func appItemFromAPI(a client.App) (appItemModel, error) {
	sync, err := appSyncValue(a.LatestSync)
	return appItemModel{
		ID:            types.StringValue(a.ID),
		Name:          types.StringValue(a.Name),
		AppVersion:    optionalString(a.AppVersion),
		Archived:      types.BoolValue(a.IsArchived),
		ArchivedAt:    optionalString(a.ArchivedAt),
		CreatedAt:     optionalString(a.CreatedAt),
		FunctionCount: types.Int64Value(a.FunctionCount),
		Method:        optionalString(a.Method),
		LatestSync:    sync,
	}, err
}

func (d *appDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_app"
}

func (d *appDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs := appComputedAttributes()
	attrs["id"] = schema.StringAttribute{
		MarkdownDescription: "The user-defined app ID (the `id` passed to the Inngest client in the app's code).",
		Required:            true,
		Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
	}
	attrs["environment"] = schema.StringAttribute{
		MarkdownDescription: "Environment of the app, sent as the `X-Inngest-Env` header. Defaults to the provider's `environment`, then to `production`.",
		Optional:            true,
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Reads one Inngest app: its sync status, SDK, function count and whether it is archived. Apps are created by syncing them from their code, not through Terraform.",
		Attributes:          attrs,
	}
}

func (d *appDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	data, diags := getProviderData(req.ProviderData)
	resp.Diagnostics.Append(diags...)
	if data != nil {
		d.data = data
	}
}

func (d *appDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg appModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	app, err := d.data.client.GetApp(ctx, d.data.headerEnv(cfg.Environment), cfg.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error reading Inngest app", err.Error())
		return
	}
	item, err := appItemFromAPI(*app)
	if err != nil {
		resp.Diagnostics.AddError("Error reading Inngest app", err.Error())
		return
	}
	state := appModel{
		Environment:   cfg.Environment,
		ID:            cfg.ID,
		Name:          item.Name,
		AppVersion:    item.AppVersion,
		Archived:      item.Archived,
		ArchivedAt:    item.ArchivedAt,
		CreatedAt:     item.CreatedAt,
		FunctionCount: item.FunctionCount,
		Method:        item.Method,
		LatestSync:    item.LatestSync,
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (d *appsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_apps"
}

func (d *appsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	item := appComputedAttributes()
	item["id"] = schema.StringAttribute{Computed: true, MarkdownDescription: "The user-defined app ID."}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists the apps of an Inngest environment.",
		Attributes: map[string]schema.Attribute{
			"environment": schema.StringAttribute{
				MarkdownDescription: "Environment to list apps for, sent as the `X-Inngest-Env` header. Defaults to the provider's `environment`, then to `production`.",
				Optional:            true,
			},
			"archived": schema.BoolAttribute{
				MarkdownDescription: "List archived apps instead of active ones. Defaults to `false`.",
				Optional:            true,
			},
			"apps": schema.ListNestedAttribute{
				MarkdownDescription: "The apps, in the order the API returns them.",
				Computed:            true,
				NestedObject:        schema.NestedAttributeObject{Attributes: item},
			},
		},
	}
}

func (d *appsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	data, diags := getProviderData(req.ProviderData)
	resp.Diagnostics.Append(diags...)
	if data != nil {
		d.data = data
	}
}

func (d *appsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg appsModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	apps, err := d.data.client.ListApps(ctx, d.data.headerEnv(cfg.Environment), cfg.Archived.ValueBool())
	if err != nil {
		resp.Diagnostics.AddError("Error listing Inngest apps", err.Error())
		return
	}
	cfg.Apps = []appItemModel{}
	for _, a := range apps {
		item, err := appItemFromAPI(a)
		if err != nil {
			resp.Diagnostics.AddError("Error listing Inngest apps", err.Error())
			return
		}
		cfg.Apps = append(cfg.Apps, item)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, cfg)...)
}
