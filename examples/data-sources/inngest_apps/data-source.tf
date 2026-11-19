data "inngest_apps" "production" {
  environment = "production"
}

output "app_function_counts" {
  value = { for a in data.inngest_apps.production.apps : a.id => a.function_count }
}
