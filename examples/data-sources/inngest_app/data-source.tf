# Warn on every plan and apply when the billing app's latest sync failed.
# A check block reports a warning; it does not stop the run.
data "inngest_app" "billing" {
  environment = "production"
  id          = "billing"
}

check "billing_app_synced" {
  assert {
    condition     = try(data.inngest_app.billing.latest_sync.status, "") != "error"
    error_message = "The billing app's latest Inngest sync failed: ${try(coalesce(data.inngest_app.billing.latest_sync.error, "no details"), "no details")}"
  }
}
