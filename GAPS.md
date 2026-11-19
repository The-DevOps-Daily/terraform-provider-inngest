# Gaps to confirm against the real API

The provider was built from Inngest's public OpenAPI specs and docs (see
[`testdata/README.md`](testdata/README.md)) and tested against a fake of the
API, without an Inngest account. These are the points the specs and docs did
not settle, what the provider assumes for each, and how to check it.

Start with `scripts/probe-api.sh [environment]`, which makes read-only calls
and prints the responses with keys and URLs redacted. Then run
`make testacc` (see the README).

## Authentication

1. **API keys work for every v2 endpoint used, on the free Hobby plan.** The
   docs say API keys are for "v2 endpoints only" and that admins create them.
   Assumed: an admin key with full access reaches `/v2/envs`, `/v2/keys/*`,
   `/v2/env/webhooks` and `/v2/apps`. Check: every probe call returns 200.
   Note which permissions a scoped key needs (the open source service
   definition names `environments:write:create`; others are not published).
2. **The v1 webhook endpoints take a signing key, raw or hashed.** The v1
   spec says "Use your environment's signing key as a bearer token". The
   provider tries each key from `GET /v2/keys/signing`, first as given, then
   hashed the way the SDKs send it (`signkey-<env>-` + hex SHA-256 of the
   hex-decoded rest). Check: `TestAccWebhook` step 2 (rename) passes, and
   with `TF_LOG=DEBUG` see which form worked. If the v1 API also accepts the
   API key, the signing key lookup could be dropped.
3. **The v1 endpoints accept the `X-Inngest-Env` header.** The provider sends
   it so branch environments, which share one signing key, pick the right
   branch. Check: `TestAccWebhook` with `INNGEST_ACC_ENVIRONMENT` set to a
   custom environment and, if one exists, a branch environment.

## Environments

4. **`X-Inngest-Env` takes the environment name.** The spec only gives
   examples (`production`, `staging`, `development`); `v2Env` has no slug
   field. Assumed: the name as returned by `GET /v2/envs`, for custom
   environments and branch names. Check: `TestAccEnvironment`'s last step reads
   keys with `environment = <new environment name>`. If the API wants a slug
   (for example lower-case with dashes), names with capitals or spaces will
   fail; the fix is an `env_header` computed attribute.
5. **`GET /v2/envs` lists archived environments with `isArchived: true`.**
   Assumed yes, since `v2Env` has the field. If archived environments are left
   out, the provider keeps an environment that is archived in state instead of
   planning it again (tested against the fake), but importing an archived
   environment would fail. Check: after `TestAccEnvironment` step 2, the probe
   shows the `tf-acc-` environment with `isArchived: true`.
6. **What `POST /v2/envs` creates.** The spec's example answers with
   `"type": "BRANCH"`; the docs call these "custom environments". `type` is
   computed, so either works. Check the type in the probe output. protobuf
   JSON leaves out an enum's zero value, which for `v2EnvType` is
   `PRODUCTION`, so an environment without `type` is read as production (and
   never archived on destroy); check that the production row in the probe
   output has no other type. The
   request's optional `id` field has no description, so it is never sent.
7. **A duplicate name answers 409.** The spec lists 409 ("Conflict - account
   already exists", copied from another endpoint). The provider adds an
   import hint on 409. Also check whether the name of an archived environment
   can be used again: create `tf-acc-x`, archive it, create `tf-acc-x` again.
8. **`PATCH /v2/envs/{id}` answers with the full environment.** The provider
   only relies on the call succeeding and keeps the other fields from state.
9. **Names come back exactly as sent.** If the API trims or changes the case
   of an environment name, the resource keeps the configured name after
   create, but the next refresh shows a diff and plans a replacement.
10. **Free plan limit.** The limits page says 3 active workspaces and 2 active
    branch deploys on the free plan, counting active resources only. Confirm
    that archived environments free a slot.

## Webhooks

11. **`GET /v2/env/webhooks` returns every field.** The provider reads a
    webhook from the v2 list (there is no v2 read-one endpoint) and needs
    `id`, `name`, `url`, `transform`, `response`, `eventFilter`,
    `environment`, `createdAt` and `updatedAt`. Check the probe output.
12. **Event filter JSON.** The service is protobuf with grpc-gateway, so
    `eventFilter.filter` may be left out when it is `ALLOW` (the enum's zero
    value) and `events` may be left out when empty. The provider reads a
    missing filter as `ALLOW` and an empty list as no filter. Check:
    `TestAccWebhook` step 4 passes with an `ALLOW` filter.
13. **`PUT /v1/webhooks/{id}` keeps the event filter and response.** The v1
    body has only `name` and `transform`. If a PUT clears `eventFilter` or
    `response`, the next plan shows a replacement. Check: after the acceptance
    run, or by hand: create a webhook with a filter, rename it, and run
    `terraform plan` (it must be empty).
14. **`DELETE /v1/webhooks/{id}` deletes webhooks made with the v2 API.**
    Check: `TestAccWebhook`'s CheckDestroy.
15. **Transform text comes back as sent.** Leading and trailing whitespace is
    ignored when comparing. Any other rewrite (for example minification)
    would show as a diff on every plan.
16. **A v2 webhook update endpoint is coming.** The open source service
    definition has `UpdateWebhook` (`POST /v2/webhooks/{id}`, name and
    transform), but the published spec does not, so the provider uses v1.
    Switch to v2 once it is published.

## Keys and apps

17. **Keys follow `X-Inngest-Env`.** Without the header the spec says the
    production keys are returned. Check that a custom environment's keys
    differ from production's, and what a branch environment returns (the
    docs say all branch environments share one signing key and event keys).
18. **The app endpoints honour `X-Inngest-Env`.** The spec does not list the
    header for `/v2/apps`, but says "the authenticated environment". The
    provider sends it when an environment is set. Check with
    `INNGEST_ACC_APP_ID` against an app in a non-production environment.

## Errors and limits

19. **Error bodies.** v2 errors are `{"errors":[{"code","message"}]}`; the
    401 example is the JSON string `"Authentication failed"`. The v1 spec has
    no error schema; the client also reads `{"error": "..."}` and plain text.
20. **Rate limits.** The docs mention 429s on bulk calls. The client retries
    429 for every method and honours `Retry-After` (seconds or a date).
