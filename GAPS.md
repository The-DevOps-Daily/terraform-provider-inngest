# Real API findings and remaining gaps

The first real-API run on **2026-11-20** confirmed the points below. The mock
API now reflects these observations where they differ from the published
specs. See [`testdata/README.md`](testdata/README.md) for the original specs.

After the fixes, the real-API acceptance suite (`TestAccEnvironment`,
`TestAccWebhook`, `TestAccDataSources` and `TestAccKeysDenied`) passed twice on
2026-11-20 in the `production` environment: once in a local `TF_ACC=1` run
and once in the
[Acceptance workflow](https://github.com/The-DevOps-Daily/terraform-provider-inngest/actions/runs/44768827231).

## Confirmed on 2026-11-20

| Operation | Observed behaviour | Provider behaviour |
| --- | --- | --- |
| `POST /v2/envs` | Creates custom environments; the response omits `type`. | Preserve missing type as null and archive custom environments on destroy. |
| `GET /v2/envs` | Lists environments. Both custom environments and the account's `production` environment omit `type`; branches report `BRANCH`. | Missing type alone does not imply production. Protect explicit `PRODUCTION`, or missing type plus the name `production` (case-insensitive). Type filters match only reported types. |
| `PATCH /v2/envs/{id}` | `{"isArchived":true}` works and returns the environment with `isArchived:true`. | Destroy archives custom environments. |
| `GET /v2/env/webhooks` | Works with `X-Inngest-Env`; without it an API key with access to all environments receives 400. | Always send the environment header, defaulting to `production`, on all environment-scoped calls. |
| `POST /v2/env/webhooks` | Webhook creation works with the environment header. | Keep v2 creation. |
| `GET /v2/apps` | App listing works. | Keep the app data sources. |
| `GET /v2/keys/events` and `GET /v2/keys/signing` | API keys receive 403 `access_denied`: `API key does not allow this operation`. The signing endpoint can also return 404 `env_not_found` without the environment header. | Keep key data sources with clear credential-access diagnostics. Always send the header. |
| `PUT /v1/webhooks/{id}` | Works with the environment's dashboard signing key, sent as given (raw). A rename and transform change updates in place and keeps the URL. | Keep v1 updates with a supplied signing key. |
| `DELETE /v1/webhooks/{id}` | Works with the raw signing key and removes a v2-created webhook, both for replacements and for `terraform destroy`. | Keep v1 deletes with a supplied signing key. |
| `X-Inngest-Env` on v1 | Not needed with a signing key in the 2026-11-20 runs; the key selects the environment. | The provider still sends it, because branch environments share one signing key. |
| Dashboard API-key permissions | There is no keys permission. Resources shown are Accounts, Apps, Environments, Events, Experiments, Feedback, Functions, Images, Insights, Runs, Sandboxes, Sessions and Webhooks. | Do not suggest granting an API key signing-key read permission. Accept a dashboard signing key for v1 webhook calls. |

## Still open: webhook details outside the acceptance suite

- **Other environments:** the v1 calls were confirmed in `production` only.
  Custom and branch environments, especially branches that share a signing
  key, are not yet tested.
- **Field preservation:** the acceptance test renames a webhook and changes its
  transform in one update. Whether a rename alone keeps an existing event
  filter and response is not tested.
- **Hashed key form:** the raw signing key worked, so the SDK-hashed fallback
  has not been needed against the real API.

## Other unresolved details

- Whether archived environments remain in `GET /v2/envs` and whether archiving
  reliably frees an active-environment slot. The provider tolerates an archived
  environment disappearing from the list when it is already archived in state.
- Whether unarchive behaves consistently; duplicate-name responses and reuse
  of archived names; exact preservation of names containing capitals or spaces.
  Environment rename remains unsupported by the published API, so changing
  `name` replaces the resource.
- Whether webhook lists return every optional field, and how empty event
  filters and transform whitespace are normalized. Mocks cover omitted `ALLOW`
  and empty filters, but real behaviour still needs verification.
- Successful key reads with a credential that has key access, including branch
  key sharing. API keys are confirmed unsuitable; an alternative credential
  flow has not been verified.
- App read-by-ID and environment selection outside the tested environment.
- v1 error shapes, rate limits and retry semantics under real failures. The
  confirmed v2 key denial uses `{"errors":[{"code","message"}]}`.
- A published v2 webhook update/delete API. Continue using v1 until available.

Use `scripts/probe-api.sh [environment]` for redacted read-only diagnostics.
Only run `make testacc` intentionally with real credentials; it creates and
archives environments and creates, updates and deletes webhooks. Set the
matching `INNGEST_SIGNING_KEY` before running webhook acceptance tests.
