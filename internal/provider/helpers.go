package provider

import (
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

// objectAsOptions is used to read nested objects into Go structs.
var objectAsOptions = basetypes.ObjectAsOptions{}

// getProviderData unpacks what the provider's Configure passed on. It returns
// nil without an error when the provider is not configured yet.
func getProviderData(data any) (*providerData, diag.Diagnostics) {
	var diags diag.Diagnostics
	if data == nil {
		return nil, diags
	}
	pd, ok := data.(*providerData)
	if !ok {
		diags.AddError("Unexpected provider data", fmt.Sprintf("Expected *providerData, got %T. Please report this issue to the provider developers.", data))
		return nil, diags
	}
	return pd, diags
}

// headerEnv picks the X-Inngest-Env value for a data source: its own
// environment, else the provider's, else "" (the API then uses production).
func (pd *providerData) headerEnv(attr types.String) string {
	if !attr.IsNull() && !attr.IsUnknown() && attr.ValueString() != "" {
		return attr.ValueString()
	}
	return pd.environment
}

// resourceEnv picks the environment of a resource that needs one: its own,
// else the provider's, else production.
func (pd *providerData) resourceEnv(attr types.String) string {
	if env := pd.headerEnv(attr); env != "" {
		return env
	}
	return defaultEnvironment
}

// optionalString maps "" to null.
func optionalString(s string) types.String {
	if s == "" {
		return types.StringNull()
	}
	return types.StringValue(s)
}

// sameCode reports whether two pieces of code are equal apart from leading
// and trailing whitespace, which an editor or heredoc often adds.
func sameCode(a, b string) bool {
	return strings.TrimSpace(a) == strings.TrimSpace(b)
}

// splitImportID splits "<environment>/<id>" at the last slash, since branch
// names can contain slashes and IDs cannot. Without a slash, env is "".
func splitImportID(raw string) (env, id string) {
	i := strings.LastIndex(raw, "/")
	if i < 0 {
		return "", raw
	}
	return raw[:i], raw[i+1:]
}
