# Real API findings and remaining gaps

The first real-API run on **2026-11-20** confirmed the points below. The mock
API now reflects these observations where they differ from the published
specs. See [`testdata/README.md`](testdata/README.md) for the original specs.
No further real-API validation was performed for these fixes.

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
| Dashboard API-key permissions | There is no keys permission. Resources shown are Accounts, Apps, Environments, Events, Experiments, Feedback, Functions, Images, Insights, Runs, Sandboxes, Sessions and Webhooks. | Do not suggest granting an API key signing-key read permission. Accept a dashboard signing key for v1 webhook calls. |

## Still open: webhook updates, deletes and rename behaviour

- **v1 authentication:** verify `PUT /v1/webhooks/{id}` and
  `DELETE /v1/webhooks/{id}` using an environment signing key from the
  dashboard. Earlier attempts failed during the signing-key lookup; they did
  not establish whether these calls work. The provider now accepts provider
  `signing_key`, `INNGEST_SIGNING_KEY`, and a per-webhook override. It tries the
  raw key and the SDK-hashed form. Success is covered by mocks only.
- **Rename behaviour:** confirm that a v1 rename changes the name in place and
  preserves the URL, transform, event filter and response. Confirm a transform
  update preserves the other fields, and delete removes a v2-created webhook.
- **Environment selection for v1:** verify `X-Inngest-Env` on custom and branch
  environments, especially branches that share a signing key.

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
