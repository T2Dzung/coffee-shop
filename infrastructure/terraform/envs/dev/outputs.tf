# ==============================================================================
# Development Environment Infrastructure Outputs
# ==============================================================================

output "dev_primary_instance_public_ip" {
  description = "The public IP address of the Primary Dev Kubernetes Node EC2 instance"
  value       = module.dev_k8s_primary.public_ip
}

output "dev_secondary_instances_public_ips" {
  description = "The public IP addresses of the Secondary Dev Kubernetes Node EC2 instances"
  value       = module.dev_k8s_secondary[*].public_ip
}

output "ssh_connection_command" {
  description = "Copy-pasteable SSH command to connect to the Primary Dev Kubernetes instance"
  value       = module.dev_k8s_primary.public_ip != null ? "ssh -i '${var.private_key_path}' ubuntu@${module.dev_k8s_primary.public_ip}" : null
}

output "kubeconfig_retrieve_command" {
  description = "Command to retrieve the kubeconfig file from the Primary Dev instance to your local machine"
  value       = module.dev_k8s_primary.public_ip != null ? "scp -i '${var.private_key_path}' ubuntu@${module.dev_k8s_primary.public_ip}:/home/ubuntu/.kube/config ./dev-kubeconfig" : null
}

output "dev_cluster_status" {
  description = "The deployment status of the Dev HA K3s Cluster"
  value       = module.dev_k8s_primary.public_ip != null ? "Dev HA Cluster deployed. Primary K3s node is bootstrapping, and Secondary nodes are waiting to join. Please wait 3-5 minutes for the entire HA cluster to become ready." : "Deployment in progress."
}
