# Creates an environment and a Stripe webhook. API keys cannot read event or
# signing keys; obtain them from the environment dashboard after creation.
# Before updating/deleting the webhook, set INNGEST_SIGNING_KEY to its key.

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

output "environment_id" {
  value = inngest_environment.this.id
}

output "stripe_webhook_url" {
  description = "Endpoint URL to give to Stripe."
  value       = inngest_webhook.stripe.url
  sensitive   = true
}
