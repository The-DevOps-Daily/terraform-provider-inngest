terraform {
  required_providers {
    inngest = {
      source = "the-devops-daily/inngest"
    }
  }
}

# Reads INNGEST_API_KEY from the environment. Create an API key at
# https://app.inngest.com/settings/api-keys.
provider "inngest" {
  # Default environment for every resource and data source, sent as the
  # X-Inngest-Env header. Each one can override it.
  environment = "production"
}
