package provider

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/The-DevOps-Daily/terraform-provider-inngest/internal/client"
)

var (
	_ resource.Resource                   = &webhookResource{}
	_ resource.ResourceWithConfigure      = &webhookResource{}
	_ resource.ResourceWithImportState    = &webhookResource{}
	_ resource.ResourceWithModifyPlan     = &webhookResource{}
	_ resource.ResourceWithValidateConfig = &webhookResource{}
)

// NewWebhookResource returns a new inngest_webhook resource.
func NewWebhookResource() resource.Resource {
	return &webhookResource{}
}

type webhookResource struct {
	data *providerData
}

type webhookResourceModel struct {
	ID          types.String `tfsdk:"id"`
	Environment types.String `tfsdk:"environment"`
	Name        types.String `tfsdk:"name"`
	Transform   types.String `tfsdk:"transform"`
	Response    types.String `tfsdk:"response"`
	EventFilter types.Object `tfsdk:"event_filter"`
	URL         types.String `tfsdk:"url"`
	CreatedAt   types.String `tfsdk:"created_at"`
	UpdatedAt   types.String `tfsdk:"updated_at"`
}

type eventFilterModel struct {
	Filter types.String `tfsdk:"filter"`
	Events types.Set    `tfsdk:"events"`
}

var eventFilterAttrTypes = map[string]attr.Type{
	"filter": types.StringType,
	"events": types.SetType{ElemType: types.StringType},
}

func (r *webhookResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_webhook"
}

func (r *webhookResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "An Inngest webhook: a URL that accepts HTTP requests from a third party (Stripe, GitHub, Clerk, ...) and turns each one into an Inngest event with a JavaScript `transform` function.\n\n" +
			"Give the generated `url` to the provider that sends the webhooks. The URL is a secret: anyone who has it can send events into the environment.\n\n" +
			"The v2 API creates and lists webhooks. Renaming a webhook, changing its transform and deleting it go through the v1 API, which takes the environment's signing key, " +
			"so the provider lists the environment's signing keys with the API key first. The API key therefore needs permission to read signing keys. " +
			"`response` and `event_filter` cannot be changed in place, so changing them replaces the webhook, and the replacement has a new `url`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Webhook ID.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"environment": schema.StringAttribute{
				MarkdownDescription: "Environment of the webhook, sent as the `X-Inngest-Env` header. Defaults to the provider's `environment`, then to `production`. Changing it replaces the webhook.",
				Optional:            true,
				Computed:            true,
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Descriptive name, at most 128 characters.",
				Required:            true,
				Validators:          []validator.String{stringvalidator.LengthBetween(1, 128)},
			},
			"transform": schema.StringAttribute{
				MarkdownDescription: "JavaScript transform function for incoming requests, for example " +
					"`function transform(evt, headers = {}, queryParams = {}, raw = '') { return { name: 'stripe/' + evt.type, data: evt }; }`. " +
					"It must return an object with an event `name` and `data`. Leading and trailing whitespace is ignored when comparing with the API.",
				Required:   true,
				Validators: []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"response": schema.StringAttribute{
				MarkdownDescription: "Optional JavaScript function that answers GET requests to the webhook URL, for providers that verify an endpoint with a challenge. Changing it replaces the webhook.",
				Optional:            true,
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"event_filter": schema.SingleNestedAttribute{
				MarkdownDescription: "Optional filter on the event names the transform produces. Changing it replaces the webhook.",
				Optional:            true,
				PlanModifiers:       []planmodifier.Object{objectplanmodifier.RequiresReplace()},
				Attributes: map[string]schema.Attribute{
					"filter": schema.StringAttribute{
						MarkdownDescription: "`ALLOW` accepts only the listed events; `DENY` drops the listed events.",
						Required:            true,
						Validators:          []validator.String{stringvalidator.OneOf(client.FilterTypes...)},
					},
					"events": schema.SetAttribute{
						MarkdownDescription: "Event names or patterns, such as `orders/payment.created` or `orders/*`.",
						Required:            true,
						ElementType:         types.StringType,
						Validators:          []validator.Set{setvalidator.SizeAtLeast(1), setvalidator.ValueStringsAre(stringvalidator.LengthAtLeast(1))},
					},
				},
			},
			"url": schema.StringAttribute{
				MarkdownDescription: "The generated webhook URL to give to the sending service. Sensitive, since anyone with it can send events.",
				Computed:            true,
				Sensitive:           true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"created_at": schema.StringAttribute{
				MarkdownDescription: "Creation time (RFC 3339).",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"updated_at": schema.StringAttribute{
				MarkdownDescription: "Last update time (RFC 3339).",
				Computed:            true,
			},
		},
	}
}

func (r *webhookResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	data, diags := getProviderData(req.ProviderData)
	resp.Diagnostics.Append(diags...)
	if data != nil {
		r.data = data
	}
}

func (r *webhookResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var transform types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("transform"), &transform)...)
	if !transform.IsNull() && !transform.IsUnknown() && strings.TrimSpace(transform.ValueString()) == "" {
		resp.Diagnostics.AddAttributeError(path.Root("transform"), "Empty transform", "transform must contain a JavaScript function.")
	}
}

// ModifyPlan resolves the environment at plan time, so that a webhook moved to
// another environment (or a change of the provider default) shows as a
// replacement.
func (r *webhookResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() || r.data == nil {
		return
	}
	var configured types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("environment"), &configured)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if configured.IsUnknown() {
		// The environment is only known at apply time. It may differ from
		// the current one, so plan a replacement now: deciding at apply
		// time would change the planned action, which Terraform refuses.
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("environment"), types.StringUnknown())...)
		if !req.State.Raw.IsNull() {
			resp.RequiresReplace = append(resp.RequiresReplace, path.Root("environment"))
		}
		return
	}
	want := types.StringValue(r.data.resourceEnv(configured))
	resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("environment"), want)...)
	if req.State.Raw.IsNull() {
		return
	}
	var current types.String
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("environment"), &current)...)
	if !current.IsNull() && !current.Equal(want) {
		resp.RequiresReplace = append(resp.RequiresReplace, path.Root("environment"))
	}
}

func (r *webhookResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan webhookResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	env := r.data.resourceEnv(plan.Environment)
	plan.Environment = types.StringValue(env)

	body := client.CreateWebhookRequest{
		Name:      plan.Name.ValueString(),
		Transform: plan.Transform.ValueString(),
		Response:  plan.Response.ValueString(),
	}
	filter, diags := expandEventFilter(ctx, plan.EventFilter)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	body.EventFilter = filter

	wh, err := r.data.client.CreateWebhook(ctx, env, body)
	if err != nil {
		resp.Diagnostics.AddError("Error creating Inngest webhook", err.Error())
		return
	}
	if wh.ID == "" {
		resp.Diagnostics.AddError("Error creating Inngest webhook", "The API answered without a webhook ID.")
		return
	}
	resp.Diagnostics.Append(mapWebhook(ctx, wh, &plan)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *webhookResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state webhookResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	env := r.data.resourceEnv(state.Environment)
	state.Environment = types.StringValue(env)

	wh, err := r.data.client.GetWebhook(ctx, env, state.ID.ValueString())
	if err != nil {
		if client.IsMissing(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error reading Inngest webhook", err.Error())
		return
	}
	resp.Diagnostics.Append(mapWebhook(ctx, wh, &state)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *webhookResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state webhookResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	env := state.Environment.ValueString()
	plan.Environment = state.Environment
	id := state.ID.ValueString()

	if !plan.Name.Equal(state.Name) || !sameCode(plan.Transform.ValueString(), state.Transform.ValueString()) {
		err := r.data.client.UpdateWebhook(ctx, env, id, client.UpdateWebhookRequest{
			Name:      plan.Name.ValueString(),
			Transform: plan.Transform.ValueString(),
		})
		if err != nil {
			resp.Diagnostics.AddError("Error updating Inngest webhook", err.Error())
			return
		}
	}

	wh, err := r.data.client.GetWebhook(ctx, env, id)
	if err != nil {
		resp.Diagnostics.AddError("Error reading Inngest webhook after update", err.Error())
		return
	}
	resp.Diagnostics.Append(mapWebhook(ctx, wh, &plan)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *webhookResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state webhookResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	env := r.data.resourceEnv(state.Environment)
	if err := r.data.client.DeleteWebhook(ctx, env, state.ID.ValueString()); err != nil {
		if client.IsNotFound(err) {
			return
		}
		resp.Diagnostics.AddError("Error deleting Inngest webhook", err.Error())
	}
}

// ImportState accepts "<environment>/<id>", or "<id>" for a webhook in the
// provider's default environment.
func (r *webhookResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	env, id := splitImportID(strings.TrimSpace(req.ID))
	if id == "" {
		resp.Diagnostics.AddError("Invalid import ID", fmt.Sprintf("Expected \"<environment>/<webhook id>\" or \"<webhook id>\", got %q.", req.ID))
		return
	}
	if env == "" && r.data != nil {
		env = r.data.resourceEnv(types.StringNull())
	}
	if env == "" {
		env = defaultEnvironment
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), id)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("environment"), env)...)
}

// expandEventFilter turns the event_filter attribute into the API shape.
func expandEventFilter(ctx context.Context, obj types.Object) (*client.EventFilter, diag.Diagnostics) {
	var diags diag.Diagnostics
	if obj.IsNull() || obj.IsUnknown() {
		return nil, diags
	}
	var m eventFilterModel
	diags.Append(obj.As(ctx, &m, objectAsOptions)...)
	if diags.HasError() {
		return nil, diags
	}
	var events []string
	diags.Append(m.Events.ElementsAs(ctx, &events, false)...)
	sort.Strings(events)
	return &client.EventFilter{Filter: m.Filter.ValueString(), Events: events}, diags
}

// mapWebhook copies what the API reported onto the model. The transform and
// response keep the configured text when the API's copy differs only in
// surrounding whitespace.
func mapWebhook(ctx context.Context, wh *client.Webhook, m *webhookResourceModel) diag.Diagnostics {
	var diags diag.Diagnostics
	m.ID = types.StringValue(wh.ID)
	m.Name = types.StringValue(wh.Name)
	if m.Transform.IsNull() || m.Transform.IsUnknown() || !sameCode(m.Transform.ValueString(), wh.Transform) {
		m.Transform = types.StringValue(wh.Transform)
	}
	switch {
	case wh.Response == "":
		m.Response = types.StringNull()
	case m.Response.IsNull() || m.Response.IsUnknown() || !sameCode(m.Response.ValueString(), wh.Response):
		m.Response = types.StringValue(wh.Response)
	}
	if wh.Environment != "" && m.Environment.IsNull() {
		m.Environment = types.StringValue(wh.Environment)
	}
	m.URL = optionalString(wh.URL)
	if wh.CreatedAt != "" {
		m.CreatedAt = types.StringValue(wh.CreatedAt)
	} else if m.CreatedAt.IsUnknown() {
		m.CreatedAt = types.StringNull()
	}
	m.UpdatedAt = optionalString(wh.UpdatedAt)

	filter := normalizeFilter(wh.EventFilter)
	if filter == nil {
		m.EventFilter = types.ObjectNull(eventFilterAttrTypes)
		return diags
	}
	events, d := types.SetValueFrom(ctx, types.StringType, filter.Events)
	diags.Append(d...)
	obj, d := types.ObjectValue(eventFilterAttrTypes, map[string]attr.Value{
		"filter": types.StringValue(filter.Filter),
		"events": events,
	})
	diags.Append(d...)
	m.EventFilter = obj
	return diags
}

// normalizeFilter treats a filter with no events as no filter, and a filter
// with no type as ALLOW, the enum's default value (protobuf JSON leaves
// default values out of responses).
func normalizeFilter(f *client.EventFilter) *client.EventFilter {
	if f == nil || len(f.Events) == 0 {
		return nil
	}
	out := &client.EventFilter{Filter: strings.ToUpper(f.Filter), Events: f.Events}
	if out.Filter == "" {
		out.Filter = client.FilterAllow
	}
	return out
}
