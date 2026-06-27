output "instance_id" {
  description = "The ID of the allocated EC2 instance"
  value       = module.ec2.id
}

output "public_ip" {
  description = "The public IP address of the EC2 instance"
  value       = module.ec2.public_ip
}

output "private_ip" {
  description = "The private IP address of the EC2 instance"
  value       = module.ec2.private_ip
}

output "ssh_command" {
  description = "Convenience SSH command to connect to the instance"
  value       = module.ec2.public_ip != null ? "ssh -i '<path-to-key>' ubuntu@${module.ec2.public_ip}" : null
}
