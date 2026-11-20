# Complete example

Creates an Inngest environment and a Stripe webhook in one apply.

```sh
export INNGEST_API_KEY=sk-inn-api-...
terraform init
terraform apply -var environment_name=staging
terraform output -raw stripe_webhook_url
```

After creation, obtain the environment's event and signing keys from the
Inngest dashboard. API keys cannot read either key endpoint (403), and the
API-key settings have no keys permission.

Before updating or destroying the webhook, set `INNGEST_SIGNING_KEY` to this
new environment's signing key. Alternatively set `signing_key` on the provider
or webhook using a sensitive variable. A webhook override takes precedence.
Set `INNGEST_EVENT_KEY` and `INNGEST_SIGNING_KEY` in your application's runtime
using the keys from the dashboard.

`terraform destroy` deletes the webhook and archives the environment. The v1
webhook update/delete flow with a supplied signing key is covered by mocks;
real-API confirmation remains open in [`GAPS.md`](../../GAPS.md).
