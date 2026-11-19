# Terraform Provider for Inngest

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
| Data source | `inngest_event_keys` | Event keys of an environment (for `INNGEST_EVENT_KEY`). |
| Data source | `inngest_signing_keys` | Signing keys of an environment (for `INNGEST_SIGNING_KEY`). |
| Data source | `inngest_app` | One app: sync status, SDK, function count. |
| Data source | `inngest_apps` | Active or archived apps of an environment. |
| Data source | `inngest_webhooks` | All webhooks of an environment. |

Not covered, because the public API has no endpoint for it: creating,
rotating or deleting event keys and signing keys, deleting or renaming
environments, and managing API keys. Those stay in the dashboard.

Everything here is built to work on the free Hobby plan: the docs put none of
these endpoints behind a paid plan, and the real-API tests are sized for it.
The free plan allows 3 active environments and 2 active branch environments;
archived ones do not count.

## Quick start

```hcl
terraform {
  required_providers {
    inngest = {
      source = "the-devops-daily/inngest"
    }
  }
}

# Reads INNGEST_API_KEY. Create a key at https://app.inngest.com/settings/api-keys.
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

data "inngest_signing_keys" "staging" {
  environment = inngest_environment.staging.name
  depends_on  = [inngest_environment.staging]
}

output "stripe_webhook_url" {
  value     = inngest_webhook.stripe.url
  sensitive = true
}
```

[`examples/complete`](examples/complete) is the full version: a new
environment, a Stripe webhook in it, and its event key and signing key as
outputs, in one apply. Every resource and data source has an example under
[`examples/`](examples) and a page under [`docs/`](docs).

## Configuration

| Argument | Environment variable | Default |
| --- | --- | --- |
| `api_key` | `INNGEST_API_KEY` | none (sensitive) |
| `base_url` | `INNGEST_API_BASE_URL` | `https://api.inngest.com` |
| `environment` | `INNGEST_ENV` | none |

### Environments and the `X-Inngest-Env` header

Most Inngest API calls act on one environment, which the request names in the
`X-Inngest-Env` header. Every resource and data source that works inside an
environment has an `environment` argument. When it is not set, the provider's
`environment` is used. When neither is set, webhooks go to `production` and
data sources leave the header out, so the API uses the production
environment.

Use the environment name: `production`, a custom environment such as
`staging`, or a branch name such as `feature/new-checkout`. Changing a
webhook's environment (including through the provider default) replaces it.

### How webhook updates work

The v2 API can create and list webhooks but not update or delete them. The v1
API can, but it takes the environment's signing key instead of an API key.
For a rename, a transform change or a delete, the provider lists the
environment's signing keys with the API key and calls the v1 endpoint with the
key that works (as given, then in the hashed form the Inngest SDKs send). The
API key therefore needs read access to signing keys. `event_filter` and
`response` have no update endpoint, so changing them replaces the webhook,
and the new webhook has a new URL.

### Behaviour notes

- Destroying an `inngest_environment` archives it. A production environment
  is never archived; destroying it only removes it from the state.
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
published OpenAPI specs (see [`testdata/README.md`](testdata/README.md)). They
cover create, in-place update, replacement, import, resources deleted outside
Terraform, 429 retries, pagination and API errors. CI runs them on every push.

With the specs downloaded (`make spec`), the client tests also check every
endpoint, JSON field and enum the provider uses against the published specs.

### Acceptance tests against the real API

The `TestAcc*` tests use a real Inngest account. They skip unless both
`TF_ACC=1` and `INNGEST_API_KEY` are set, use random `tf-acc-` names and clean
up after themselves. They fit the free Hobby plan.

```sh
export INNGEST_API_KEY=sk-inn-api-...
# Optional: environment for the webhook and data source tests (default production)
export INNGEST_ACC_ENVIRONMENT=production
# Optional: the ID of an app synced in that environment, to test inngest_app
export INNGEST_ACC_APP_ID=my-app
make testacc
```

`TestAccEnvironment` creates one environment and archives it at the end (the
API cannot delete environments), so the account needs a free active
environment slot. Archived `tf-acc-` environments can be left as they are.

[`GAPS.md`](GAPS.md) lists what the public docs did not settle and what the
first real-API run has to confirm.

## License

[MPL-2.0](LICENSE)
