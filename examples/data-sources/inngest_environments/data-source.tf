# Every active branch environment, for example to find stale previews.
data "inngest_environments" "branches" {
  type = "BRANCH"
}

output "branch_environments" {
  value = [for e in data.inngest_environments.branches.environments : e.name]
}
