moved {
  from = aws_resourcegroups_group.bootstrap_group
  to   = module.backend.aws_resourcegroups_group.bootstrap_group
}

moved {
  from = aws_kms_key.state_key
  to   = module.backend.aws_kms_key.state_key
}

moved {
  from = aws_kms_alias.state_key_alias
  to   = module.backend.aws_kms_alias.state_key_alias
}

moved {
  from = aws_s3_bucket.terraform_state
  to   = module.backend.aws_s3_bucket.terraform_state
}

moved {
  from = aws_s3_bucket_versioning.terraform_state
  to   = module.backend.aws_s3_bucket_versioning.terraform_state
}

moved {
  from = aws_s3_bucket_server_side_encryption_configuration.terraform_state
  to   = module.backend.aws_s3_bucket_server_side_encryption_configuration.terraform_state
}

moved {
  from = aws_s3_bucket_public_access_block.terraform_state
  to   = module.backend.aws_s3_bucket_public_access_block.terraform_state
}

moved {
  from = aws_iam_role.backend_role
  to   = module.backend.aws_iam_role.backend_role
}

moved {
  from = aws_iam_policy.backend_policy
  to   = module.backend.aws_iam_policy.backend_policy
}

moved {
  from = aws_iam_role_policy_attachment.backend_attach
  to   = module.backend.aws_iam_role_policy_attachment.backend_attach
}
