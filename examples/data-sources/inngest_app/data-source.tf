# Fail the run when the billing app's latest sync is not healthy.
data "inngest_app" "billing" {
  environment = "production"
  id          = "billing"
}

check "billing_app_synced" {
  assert {
    condition     = try(data.inngest_app.billing.latest_sync.status, "") != "error"
    error_message = "The billing app's latest Inngest sync failed: ${try(data.inngest_app.billing.latest_sync.error, "")}"
  }
}
