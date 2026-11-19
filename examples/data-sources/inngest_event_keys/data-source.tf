data "inngest_event_keys" "staging" {
  environment = "staging"
}

# The first event key, for INNGEST_EVENT_KEY in the app that sends events.
output "staging_event_key" {
  value     = data.inngest_event_keys.staging.keys[0].key
  sensitive = true
}
