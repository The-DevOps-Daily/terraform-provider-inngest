# All webhooks of an environment, including ones made in the dashboard.
data "inngest_webhooks" "production" {
  environment = "production"
}

output "webhook_names" {
  value = [for w in data.inngest_webhooks.production.webhooks : w.name]
}
