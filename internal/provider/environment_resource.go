package provider

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/The-DevOps-Daily/terraform-provider-inngest/internal/client"
)

var (
	_ resource.Resource                = &environmentResource{}
	_ resource.ResourceWithConfigure   = &environmentResource{}
	_ resource.ResourceWithImportState = &environmentResource{}
)

// NewEnvironmentResource returns a new inngest_environment resource.
func NewEnvironmentResource() resource.Resource {
	return &environmentResource{}
}

type environmentResource struct {
	data *providerData
}

type environmentResourceModel struct {
	ID        types.String `tfsdk:"id"`
	Name      types.String `tfsdk:"name"`
	Type      types.String `tfsdk:"type"`
	Archived  types.Bool   `tfsdk:"archived"`
	CreatedAt types.String `tfsdk:"created_at"`
}

func (r *environmentResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_environment"
}

func (r *environmentResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "An Inngest environment, such as a shared `staging` or `qa` environment. Each environment has its own apps, functions, events, event keys and signing keys.\n\n" +
			"The Inngest API can create an environment and archive or unarchive it, but it cannot rename or delete one. So:\n\n" +
			"- Changing `name` replaces the environment: Terraform creates a new one and archives the old one.\n" +
			"- Destroying the resource archives the environment. Archiving stops its functions from triggering and keeps its data. " +
			"A production environment is never archived: destroying it only removes it from the Terraform state.\n\n" +
			"Branch environments are created by Inngest on demand, the first time an app syncs or an event is sent with that branch name. " +
			"Import one to archive or unarchive it from Terraform, or read it with the `inngest_environment` data source.\n\n" +
			"Only active environments count toward the plan's environment limit (3 active environments and 2 active branch environments on the free plan).",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Environment ID (a UUID).",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Environment name, for example `staging`. The API cannot rename an environment, so changing it replaces the environment.",
				Required:            true,
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"type": schema.StringAttribute{
				MarkdownDescription: "Environment type: `PRODUCTION`, `TEST` or `BRANCH`.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"archived": schema.BoolAttribute{
				MarkdownDescription: "Whether the environment is archived. An archived environment keeps its data, but its functions do not trigger. " +
					"When omitted, Terraform does not manage the flag: an environment archived or unarchived in the dashboard (or by branch auto-archive) stays that way.",
				Optional:      true,
				Computed:      true,
				PlanModifiers: []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
			},
			"created_at": schema.StringAttribute{
				MarkdownDescription: "Creation time (RFC 3339).",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (r *environmentResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	data, diags := getProviderData(req.ProviderData)
	resp.Diagnostics.Append(diags...)
	if data != nil {
		r.data = data
	}
}

func (r *environmentResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan environmentResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	env, err := r.data.client.CreateEnv(ctx, client.CreateEnvRequest{Name: plan.Name.ValueString()})
	if err != nil {
		detail := err.Error()
		if client.IsStatus(err, http.StatusConflict) {
			detail += "\n\nAn environment with this name may already exist, possibly archived. Import it with `terraform import` instead of creating it."
		}
		resp.Diagnostics.AddError("Error creating Inngest environment", detail)
		return
	}
	if env.ID == "" {
		resp.Diagnostics.AddError("Error creating Inngest environment", "The API answered without an environment ID.")
		return
	}

	wantArchived := !plan.Archived.IsNull() && !plan.Archived.IsUnknown() && plan.Archived.ValueBool()
	// The configured name stays in state even if the API answers with
	// another spelling; the next refresh shows any difference as drift.
	env.Name = plan.Name.ValueString()
	mapEnvironment(env, &plan)
	if wantArchived && !env.IsArchived {
		// Save the new environment first, so a failed archive does not leak it.
		resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
		patched, err := r.data.client.PatchEnv(ctx, env.ID, client.PatchEnvRequest{IsArchived: boolPtr(true)})
		if err != nil {
			resp.Diagnostics.AddError("Error archiving Inngest environment", err.Error())
			return
		}
		mergePatchedEnv(patched, env)
		mapEnvironment(env, &plan)
		plan.Archived = types.BoolValue(true)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *environmentResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state environmentResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	env, err := r.data.client.GetEnv(ctx, state.ID.ValueString())
	if err != nil {
		if client.IsNotFound(err) {
			if state.Archived.ValueBool() {
				// The list may leave archived environments out. One that is
				// archived in state is kept rather than planned again.
				return
			}
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error reading Inngest environment", err.Error())
		return
	}
	mapEnvironment(env, &state)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *environmentResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state environmentResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if plan.Archived.IsUnknown() || plan.Archived.IsNull() {
		plan.Archived = state.Archived
	}
	if !plan.Archived.Equal(state.Archived) {
		want := plan.Archived.ValueBool()
		patched, err := r.data.client.PatchEnv(ctx, state.ID.ValueString(), client.PatchEnvRequest{IsArchived: &want})
		if err != nil {
			resp.Diagnostics.AddError("Error updating Inngest environment", err.Error())
			return
		}
		env := &client.Env{ID: state.ID.ValueString(), Name: state.Name.ValueString(), Type: state.Type.ValueString(), CreatedAt: state.CreatedAt.ValueString()}
		mergePatchedEnv(patched, env)
		mapEnvironment(env, &plan)
		plan.Archived = types.BoolValue(want)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *environmentResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state environmentResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if state.Type.ValueString() == client.EnvTypeProduction {
		resp.Diagnostics.AddWarning("Production environment not archived",
			fmt.Sprintf("Environment %q is the production environment, so Terraform only removed it from the state.", state.Name.ValueString()))
		return
	}
	if state.Archived.ValueBool() {
		return
	}
	if _, err := r.data.client.PatchEnv(ctx, state.ID.ValueString(), client.PatchEnvRequest{IsArchived: boolPtr(true)}); err != nil {
		if client.IsNotFound(err) {
			return
		}
		resp.Diagnostics.AddError("Error archiving Inngest environment", err.Error()+
			"\n\nThe Inngest API cannot delete environments, so destroying an inngest_environment archives it.")
	}
}

// ImportState accepts an environment ID or an exact environment name.
func (r *environmentResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if r.data == nil {
		resp.Diagnostics.AddError("Provider not configured", "The inngest provider must be configured before importing.")
		return
	}
	raw := strings.TrimSpace(req.ID)
	envs, err := r.data.client.ListEnvs(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Error listing Inngest environments", err.Error())
		return
	}
	var byName []client.Env
	for _, e := range envs {
		if e.ID == raw {
			resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), e.ID)...)
			return
		}
		if e.Name == raw {
			byName = append(byName, e)
		}
	}
	switch len(byName) {
	case 1:
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), byName[0].ID)...)
	case 0:
		resp.Diagnostics.AddError("Environment not found", fmt.Sprintf("No Inngest environment has the ID or name %q.", raw))
	default:
		resp.Diagnostics.AddError("Ambiguous environment name", fmt.Sprintf("%d environments are named %q. Import by ID instead.", len(byName), raw))
	}
}

// mapEnvironment copies what the API reported onto the model.
func mapEnvironment(env *client.Env, m *environmentResourceModel) {
	m.ID = types.StringValue(env.ID)
	if env.Name != "" {
		m.Name = types.StringValue(env.Name)
	}
	if env.Type != "" {
		m.Type = types.StringValue(env.Type)
	} else if m.Type.IsUnknown() {
		m.Type = types.StringNull()
	}
	m.Archived = types.BoolValue(env.IsArchived)
	if env.CreatedAt != "" {
		m.CreatedAt = types.StringValue(env.CreatedAt)
	} else if m.CreatedAt.IsUnknown() {
		m.CreatedAt = types.StringNull()
	}
}

// mergePatchedEnv copies the fields a PATCH response sent onto env, except the
// name, which only a refresh updates. Fields the response leaves out keep
// their value.
func mergePatchedEnv(patched, env *client.Env) {
	if patched == nil {
		return
	}
	if patched.Type != "" {
		env.Type = patched.Type
	}
	if patched.CreatedAt != "" {
		env.CreatedAt = patched.CreatedAt
	}
	env.IsArchived = patched.IsArchived
}

func boolPtr(b bool) *bool { return &b }
