output "config" {
  value = module.backend.config
}

output "ci_config" {
  value = module.backend.additional_configs["ci"]
}
