terraform {
  required_providers {
    inngest = {
      source = "the-devops-daily/inngest"
    }
  }
}

# Reads INNGEST_API_KEY from the environment. Create an API key at
# https://app.inngest.com/settings/api-keys.
# Set INNGEST_SIGNING_KEY to this environment's dashboard signing key for
# webhook updates/deletes, or set signing_key here from a sensitive variable.
provider "inngest" {
  # Default environment for every resource and data source, sent as the
  # X-Inngest-Env header. Each one can override it.
  environment = "production"
}
