data "inngest_environment" "production" {
  name = "production"
}

output "production_environment_id" {
  value = data.inngest_environment.production.id
}
