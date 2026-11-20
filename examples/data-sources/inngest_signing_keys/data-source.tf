# Requires a credential with key access. Ordinary Inngest API keys return
# 403, even with access to all resources; there is no keys permission in the
# API-key dashboard. Obtain keys from the environment dashboard instead.
data "inngest_signing_keys" "staging" {
  environment = "staging"
}

# The signing key, for INNGEST_SIGNING_KEY in the app that serves functions.
output "staging_signing_key" {
  value     = data.inngest_signing_keys.staging.keys[0].key
  sensitive = true
}
