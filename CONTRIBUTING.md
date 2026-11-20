# Contributing

Thanks for helping improve the Terraform provider for Inngest. Read the
[Code of Conduct](CODE_OF_CONDUCT.md) before taking part. Report security
issues through the private form in [SECURITY.md](SECURITY.md).

## Development setup

Install Go 1.26 or newer, as specified in `go.mod`, GNU Make and the Terraform
CLI. Put `go` and `terraform` on your `PATH`. Clone this repository, then run:

```sh
go mod download
make build
make test
```

The Go module is `github.com/The-DevOps-Daily/terraform-provider-inngest`.
Dependencies must be downloaded before working offline.

The following targets are defined in `GNUmakefile`:

| Target | Purpose |
| --- | --- |
| `make` or `make build` | Build `terraform-provider-inngest`. |
| `make install` | Install the provider with `go install`. |
| `make test` | Run unit tests and mock API acceptance tests. |
| `make testacc` | Run real-API acceptance tests with `TF_ACC=1`. |
| `make fmt` | Format Go files with `gofmt`. |
| `make vet` | Run `go vet`. |
| `make lint` | Run golangci-lint. |
| `make docs` | Generate provider documentation with tfplugindocs. |
| `make spec` | Download Inngest OpenAPI specs into the ignored `testdata/spec/` directory. |

## Unit and mock acceptance tests

Run `make test` without Inngest credentials. Leave `TF_ACC` unset to avoid
real-API tests. The `TestMock*` tests run Terraform plan, apply, import, refresh
and destroy against an in-process fake API. No API key or signing key is
needed. Tests that use Terraform skip if the CLI is missing.

To require Terraform instead of allowing those skips, run:

```sh
INNGEST_REQUIRE_TERRAFORM=1 make test
```

Run `make spec` to enable additional client checks against the published
OpenAPI specs. This download needs network access. CI downloads the specs and
sets `INNGEST_REQUIRE_SPEC=1` and `INNGEST_REQUIRE_TERRAFORM=1` for its test run.

## Real-API acceptance tests

Use a dedicated Inngest test account. These tests create and change real
resources. Load the following values through your local secret manager or
environment without committing them:

- `TF_ACC=1` enables real-API tests. `make testacc` sets this for you.
- `INNGEST_API_KEY` is an API key for the test account with access to the
  environments and webhooks under test.
- `INNGEST_SIGNING_KEY` is the dashboard signing key for the environment used
  by the webhook tests. It is required for webhook updates and cleanup.
- `INNGEST_ACC_ENVIRONMENT` optionally selects the existing environment for
  webhook and data source tests. The default is `production` in the test account.
- `INNGEST_ACC_APP_ID` optionally selects an app already synced in that
  environment to test the single-app data source.

With the credentials set, run either command:

```sh
make testacc
# Equivalent command:
TF_ACC=1 go test ./internal/provider/ -run '^TestAcc' -v -count=1 -timeout 30m
```

The tests use random names with the `tf-acc-` prefix:

- `TestAccEnvironment` creates one environment, archives and unarchives it,
  and tests import. Final cleanup archives it because the API cannot delete
  environments. The account needs a free active environment slot.
- `TestAccWebhook` creates a webhook, updates it, imports it, replaces it and
  tests recovery after deletion. Final cleanup deletes the webhook. The signing
  key must match `INNGEST_ACC_ENVIRONMENT`.
- `TestAccDataSources` only reads existing data. `TestAccKeysDenied` checks
  that ordinary API keys cannot read event or signing keys.

The Terraform test framework runs destroy cleanup and the tests check that
their environments are archived and webhooks are gone. A failed or interrupted
run can leave resources behind. After such a run, inspect the test account for
`tf-acc-` resources, archive remaining test environments and delete remaining
test webhooks. Archived test environments can remain in the account.

Tests skip without `TF_ACC=1` or `INNGEST_API_KEY`. Webhook tests also skip
without `INNGEST_SIGNING_KEY`. The Acceptance workflow runs weekly on Monday
at 06:00 UTC and through manual dispatch, never on pull requests. It requires
both keys as repository secrets and skips the test run if either is missing.
Its signing key must belong to the test account's `production` environment.

## Documentation

Edit schema descriptions and files under `examples/`, then run `make docs`.
This uses tfplugindocs to regenerate `docs/`. Include the generated files in
your pull request. CI runs the same generation command and checks for changes
in `docs/`. Do not edit generated documentation as the only source of a change.

## Commits and pull requests

- Keep changes focused. Discuss large changes in an issue first.
- Use clear commit messages that describe the change.
- Explain the problem, the resulting behavior and how you tested it in the PR.
- Add or update tests for behavior changes. Run `make fmt`, `make test`,
  `make vet` and `make lint` before requesting review.
- Update examples and regenerate documentation when schemas or usage change.
- Add an entry under the unreleased version in [CHANGELOG.md](CHANGELOG.md)
  for user-visible changes. Use the existing categories and name the affected
  resource or data source.
- Remove secrets from code, fixtures, logs and screenshots. Do not commit
  Terraform state or local credentials.

Contributions are covered by the repository's [MPL-2.0 license](LICENSE).
