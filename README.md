# Terraform Provider for Inngest

[![Test](https://github.com/The-DevOps-Daily/terraform-provider-inngest/actions/workflows/test.yml/badge.svg)](https://github.com/The-DevOps-Daily/terraform-provider-inngest/actions/workflows/test.yml)
[![Acceptance](https://github.com/The-DevOps-Daily/terraform-provider-inngest/actions/workflows/acceptance.yml/badge.svg)](https://github.com/The-DevOps-Daily/terraform-provider-inngest/actions/workflows/acceptance.yml)
[![License: MPL-2.0](https://img.shields.io/badge/license-MPL--2.0-blue.svg)](LICENSE)
[![Go 1.26](https://img.shields.io/badge/Go-1.26-00ADD8.svg)](go.mod)
[![Terraform Registry](https://img.shields.io/badge/Terraform-Registry-844FBA.svg)](https://registry.terraform.io/providers/The-DevOps-Daily/inngest/latest)

A Terraform provider for [Inngest](https://www.inngest.com) environments and
webhooks, built on the
[Terraform Plugin Framework](https://developer.hashicorp.com/terraform/plugin/framework)
and the [Inngest REST API](https://api-docs.inngest.com).

This is a community provider. It is not affiliated with or endorsed by
Inngest, Inc.

## What it manages

| Type | Name | API |
| --- | --- | --- |
| Resource | `inngest_environment` | Create, archive and unarchive custom environments; import branch environments to archive them. Destroy archives (the API has no delete). |
| Resource | `inngest_webhook` | Create webhooks with a transform, response function and event filter; rename and change the transform in place; delete. |
| Data source | `inngest_environment` | One environment by ID or name. |
| Data source | `inngest_environments` | All environments, optionally of one type (`PRODUCTION`, `TEST`, `BRANCH`). |
| Data source | `inngest_event_keys` | Event keys of an environment; requires a credential with key access (API keys receive 403). |
| Data source | `inngest_signing_keys` | Signing keys of an environment; requires a credential with key access (API keys receive 403). |
| Data source | `inngest_app` | One app: sync status, SDK, function count. |
| Data source | `inngest_apps` | Active or archived apps of an environment. |
| Data source | `inngest_webhooks` | All webhooks of an environment. |

Not covered, because the public API has no endpoint for it: creating,
rotating or deleting event keys and signing keys, deleting or renaming
environments, and managing API keys. Those stay in the dashboard.

Environment and webhook management are designed for the free Hobby plan.
Reading keys is limited by credential access, as described below.
The free plan allows 3 active environments and 2 active branch environments;
archived ones do not count.

## Quick start

```hcl
terraform {
  required_providers {
    inngest = {
      source  = "The-DevOps-Daily/inngest"
      version = "~> 0.1"
    }
  }
}

# Reads INNGEST_API_KEY. Create a key at https://app.inngest.com/settings/api-keys.
# For webhook updates/deletes, set INNGEST_SIGNING_KEY to this environment's
# signing key from the dashboard. Creation itself only needs the API key.
provider "inngest" {}

resource "inngest_environment" "staging" {
  name = "staging"
}

resource "inngest_webhook" "stripe" {
  environment = inngest_environment.staging.name
  name        = "Stripe"
  transform   = <<-EOT
    function transform(evt) {
      return { id: evt.id, name: `stripe/$${evt.type}`, data: evt };
    }
  EOT
}

output "stripe_webhook_url" {
  value     = inngest_webhook.stripe.url
  sensitive = true
}
```

[`examples/complete`](examples/complete) is the full version: a new
environment and a Stripe webhook in it. Obtain its keys from the dashboard;
provide its signing key before updating or destroying the webhook. Every resource and data source has an example under
[`examples/`](examples) and a page under [`docs/`](docs).

## Configuration

| Argument | Environment variable | Default |
| --- | --- | --- |
| `api_key` | `INNGEST_API_KEY` | none (sensitive) |
| `base_url` | `INNGEST_API_BASE_URL` | `https://api.inngest.com` |
| `environment` | `INNGEST_ENV` | `production` |
| `signing_key` | `INNGEST_SIGNING_KEY` | none (sensitive) |

### Environments and the `X-Inngest-Env` header

Most Inngest API calls act on one environment, which the request names in the
`X-Inngest-Env` header. Every resource and data source that works inside an
environment has an `environment` argument. When it is not set, the provider's
`environment` is used. When neither is set, every environment-scoped call explicitly sends
`X-Inngest-Env: production`. The webhooks list endpoint returns 400 without
this header for an API key with access to all environments.

Use the environment name: `production`, a custom environment such as
`staging`, or a branch name such as `feature/new-checkout`. Changing a
webhook's environment (including through the provider default) replaces it.

### How webhook updates work

The v2 API can create and list webhooks but not update or delete them. The v1
API can, but it takes the environment's signing key instead of an API key.
For a rename, a transform change or a delete, supply the environment's signing
key from the Inngest dashboard. The provider uses, in order:

1. `signing_key` on the `inngest_webhook` resource.
2. `signing_key` on the provider.
3. `INNGEST_SIGNING_KEY`.
4. A lookup through `/v2/keys/signing`, only if no signing key was supplied.

API keys cannot read signing keys, so the last option returns a diagnostic
explaining how to supply one. There is no keys permission on the API-key
settings page. The provider tries a supplied key as given, then in the hashed
form used by the Inngest SDKs. A wrong supplied key does not fall back to a
lookup. For multiple environments, supply each webhook's own signing key.
Signing-key attributes are sensitive; resource values are stored in Terraform
state and cannot be recovered during import. Use the provider/environment
variable for import and supply any resource override again afterwards.

`event_filter` and `response` have no update endpoint, so changing them
replaces the webhook and gives it a new URL. v1 update/delete and rename
behaviour with a supplied signing key remain unverified against the real API.

### Reading keys

`inngest_event_keys` and `inngest_signing_keys` remain available for credentials
with key access. Ordinary API keys, even with access to all resources, receive
403 `access_denied` from both endpoints. The dashboard does not offer a keys
permission, so expanding an API key's resource permissions cannot enable these
data sources. Obtain event and signing keys from the environment's dashboard.
Provider `signing_key` is for v1 webhook operations; it does not authorize these
v2 data sources.

### Behaviour notes

- Destroying an `inngest_environment` archives it. A production environment
  is protected only when its type is `PRODUCTION`, or its type is absent and
  its name is `production` (case-insensitive). Other type-less environments
  are archived on destroy. Missing types are recorded as null, not inferred.
- `archived` is optional. When omitted, Terraform leaves the flag alone, so a
  branch environment that auto-archives does not show as drift.
- A resource deleted outside Terraform is removed from the state on the next
  refresh and planned again.
- Requests that get a 429 are retried with backoff (honouring `Retry-After`).
  Reads, updates and deletes are also retried on 5xx and network errors;
  creates are not, because a failed create may have happened.
- API errors are shown with the method, path, status and the API's own error
  code and message.

## Development

Requires Go 1.26 or newer. The plan/apply tests also need the `terraform`
CLI on `PATH`.

```sh
make build     # build the provider binary
make test      # unit tests + plan/apply/import tests against a fake API
make lint      # golangci-lint
make docs      # regenerate docs/ with tfplugindocs
make spec      # download Inngest's OpenAPI specs into testdata/spec/
```

### Tests against the fake API (no key)

`make test` runs the client unit tests and the `TestMock*` tests. The
`TestMock*` tests run real `terraform` plan, apply, import, refresh and
destroy cycles against an in-process fake of the Inngest API, built from the
published OpenAPI specs and the 2026-11-20 real-API observations (see
[`testdata/README.md`](testdata/README.md)). The default mock API key receives
403 on key endpoints; successful key-access tests use a separate synthetic
credential. They
cover create, in-place update, replacement, import, resources deleted outside
Terraform, 429 retries, pagination and API errors. CI runs them on every push.

With the specs downloaded (`make spec`), the client tests also check every
endpoint, JSON field and enum the provider uses against the published specs.

### Acceptance tests against the real API

The `TestAcc*` tests use a real Inngest account. They skip unless both
`TF_ACC=1` and `INNGEST_API_KEY` are set, use random `tf-acc-` names and clean
up after themselves. Webhook tests also require `INNGEST_SIGNING_KEY` for
updates and cleanup. They fit the free Hobby plan.

```sh
export INNGEST_API_KEY=sk-inn-api-...
# Optional: environment for the webhook and data source tests (default production)
export INNGEST_ACC_ENVIRONMENT=production
# Required for webhook update/delete tests: this environment's dashboard signing key
export INNGEST_SIGNING_KEY=signkey-prod-...
# Optional: the ID of an app synced in that environment, to test inngest_app
export INNGEST_ACC_APP_ID=my-app
make testacc
```

`TestAccEnvironment` creates one environment and archives it at the end (the
API cannot delete environments), so the account needs a free active
environment slot. Archived `tf-acc-` environments can be left as they are.

[`GAPS.md`](GAPS.md) lists what the public docs did not settle and what the
2026-11-20 real-API run confirmed, and what remains open.

## Maintained by DevOps Daily

This project is maintained by [DevOps Daily](https://devops-daily.com).
Read more in the [DevOps Daily posts](https://devops-daily.com/posts).

<!-- announcement post link -->

See the [contribution guide](CONTRIBUTING.md), [security policy](SECURITY.md)
and [changelog](CHANGELOG.md).

## License

[MPL-2.0](LICENSE)
