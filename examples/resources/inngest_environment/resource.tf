# A shared staging environment with its own apps, events and keys.
resource "inngest_environment" "staging" {
  name = "staging"
}

# An environment kept for its history, with its functions switched off.
resource "inngest_environment" "legacy" {
  name     = "legacy-qa"
  archived = true
}
