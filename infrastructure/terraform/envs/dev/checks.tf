# ==============================================================================
# Terraform Validation Checks
# ==============================================================================

check "api_endpoint_provider_match" {
  assert {
    condition = (
      !var.dev_runtime_enabled ||
      (var.active_api_endpoint_provider == "haproxy" && var.create_haproxy_api_endpoint == true) ||
      (var.active_api_endpoint_provider == "nlb" && var.create_nlb_api_endpoint == true)
    )
    error_message = "The active_api_endpoint_provider must have its corresponding create_* flag set to true (e.g. if provider is haproxy, create_haproxy_api_endpoint must be true)."
  }
}
