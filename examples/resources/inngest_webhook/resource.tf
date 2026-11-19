# Turn Stripe webhooks into Inngest events named stripe/<type>. Give
# inngest_webhook.stripe.url to Stripe as the endpoint URL.
resource "inngest_webhook" "stripe" {
  environment = "production"
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

  # Only let invoice and charge events through.
  event_filter = {
    filter = "ALLOW"
    events = ["stripe/invoice.*", "stripe/charge.*"]
  }
}

output "stripe_webhook_url" {
  value     = inngest_webhook.stripe.url
  sensitive = true
}
