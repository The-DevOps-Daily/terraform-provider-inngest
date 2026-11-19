# Complete example

Creates an Inngest environment, a Stripe webhook in it, and reads the
environment's event key and signing key, so one `terraform apply` gives an app
everything it needs to send events to and serve functions from the new
environment.

```sh
export INNGEST_API_KEY=sk-inn-api-...
terraform init
terraform apply -var environment_name=staging
terraform output -raw stripe_webhook_url
```

Pass the keys on to where the app runs, for example with the GitHub provider's
`github_actions_secret` or a Kubernetes secret, instead of copying them by
hand.

Runs on the free plan. The free plan allows 3 active environments, so archive
one first if the account already has 3. `terraform destroy` deletes the
webhook and archives the environment, because the Inngest API cannot delete
environments.
