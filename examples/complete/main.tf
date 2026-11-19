# One apply gives a new Inngest environment, a webhook that turns Stripe
# requests into events in it, and the two keys the app needs there.
#
#   export INNGEST_API_KEY=sk-inn-api-...
#   terraform init && terraform apply
#   terraform output -raw stripe_webhook_url    # paste into Stripe
#   terraform output -raw inngest_event_key     # INNGEST_EVENT_KEY
#   terraform output -raw inngest_signing_key   # INNGEST_SIGNING_KEY

terraform {
  required_version = ">= 1.5"
  required_providers {
    inngest = {
      source = "the-devops-daily/inngest"
    }
  }
}

variable "environment_name" {
  description = "Name of the Inngest environment to create."
  type        = string
  default     = "staging"
}

provider "inngest" {}

resource "inngest_environment" "this" {
  name = var.environment_name
}

resource "inngest_webhook" "stripe" {
  # The environment name is sent as the X-Inngest-Env header.
  environment = inngest_environment.this.name
  name        = "Stripe"

  transform = <<-EOT
    function transform(evt, headers = {}, queryParams = {}) {
      return {
        id: evt.id,
        name: `stripe/$${evt.type}`,
        data: evt,
      };
    }
  EOT

  event_filter = {
    filter = "ALLOW"
    events = ["stripe/checkout.session.completed", "stripe/invoice.*"]
  }
}

# Both lookups refer to the new environment, so Terraform reads them after it
# exists, during the same apply.
data "inngest_event_keys" "this" {
  environment = inngest_environment.this.name
  depends_on  = [inngest_environment.this]
}

data "inngest_signing_keys" "this" {
  environment = inngest_environment.this.name
  depends_on  = [inngest_environment.this]
}

output "environment_id" {
  value = inngest_environment.this.id
}

output "stripe_webhook_url" {
  description = "Endpoint URL to give to Stripe."
  value       = inngest_webhook.stripe.url
  sensitive   = true
}

output "inngest_event_key" {
  description = "Set as INNGEST_EVENT_KEY in the app that sends events."
  value       = try(data.inngest_event_keys.this.keys[0].key, null)
  sensitive   = true
}

output "inngest_signing_key" {
  description = "Set as INNGEST_SIGNING_KEY in the app that serves functions."
  value       = try(data.inngest_signing_keys.this.keys[0].key, null)
  sensitive   = true
}
