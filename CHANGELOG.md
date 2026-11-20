# Changelog

## 0.1.0 (Unreleased)

FEATURES:

* **New Resource:** `inngest_environment` creates custom environments and manages their archive state.
* **New Resource:** `inngest_webhook` creates, updates, imports and deletes webhooks.
* **New Data Source:** `inngest_environment` reads an environment by ID or name.
* **New Data Source:** `inngest_environments` lists environments with optional type and archive filters.
* **New Data Source:** `inngest_event_keys` reads an environment's event keys with a credential that has key access.
* **New Data Source:** `inngest_signing_keys` reads an environment's signing keys with a credential that has key access.
* **New Data Source:** `inngest_app` reads one app.
* **New Data Source:** `inngest_apps` lists active or archived apps in an environment.
* **New Data Source:** `inngest_webhooks` lists webhooks in an environment.

NOTES:

* resource/inngest_environment: Destroy archives the environment. The API has no environment delete endpoint. Destroy only removes production environments from state when their type is `PRODUCTION`, or their type is absent and their name is `production` (case-insensitive).
* resource/inngest_environment: Omitting `archived` leaves the archive state unmanaged, including automatic branch archive changes.
* provider: The `signing_key` setting or `INNGEST_SIGNING_KEY` supplies credentials for v1 webhook updates and deletes. A webhook's own `signing_key` takes precedence. Signing-key attributes are sensitive, but resource values are stored in Terraform state.
* resource/inngest_webhook: Name and transform changes update in place. Changes to `environment`, `event_filter` or `response` replace the webhook. Supplied signing-key update and delete behavior still needs real-API validation.
* data-source/inngest_event_keys, data-source/inngest_signing_keys: Ordinary API keys receive access-denied errors. These data sources need a credential with key access; the provider's `signing_key` setting does not grant that access.
