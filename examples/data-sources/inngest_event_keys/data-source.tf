# Requires a credential with key access. Ordinary Inngest API keys return
# 403, even with access to all resources; there is no keys permission in the
# API-key dashboard. Obtain keys from the environment dashboard instead.
data "inngest_event_keys" "staging" {
  environment = "staging"
}

# The first event key, for INNGEST_EVENT_KEY in the app that sends events.
output "staging_event_key" {
  value     = data.inngest_event_keys.staging.keys[0].key
  sensitive = true
}
