# ==============================================================================
# RDS PostgreSQL Subnet Group & Security Group
# ==============================================================================

resource "aws_db_subnet_group" "rds" {
  name        = "${var.project_name}-${var.environment}-rds-subnet-group"
  subnet_ids  = module.vpc.private_subnet_ids
  description = "Private DB subnet group for CoffeeShop RDS PostgreSQL"

  tags = local.common_tags
}

resource "aws_security_group" "rds" {
  name        = "${var.project_name}-${var.environment}-rds-sg"
  description = "Security group for CoffeeShop RDS PostgreSQL allowing traffic only from EKS nodes"
  vpc_id      = module.vpc.vpc_id

  ingress {
    description     = "Allow PostgreSQL traffic from EKS worker nodes"
    from_port       = 5432
    to_port         = 5432
    protocol        = "tcp"
    security_groups = [module.eks_cluster.cluster_security_group_id]
  }

  egress {
    description = "Allow all outbound traffic"
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = merge(
    local.common_tags,
    {
      Name = "${var.project_name}-${var.environment}-rds-sg"
    }
  )
}

# ==============================================================================
# RDS PostgreSQL Instance (Single-AZ, Encrypted, Private)
# ==============================================================================

resource "aws_db_instance" "postgres" {
  identifier        = "${var.project_name}-${var.environment}-db"
  engine            = "postgres"
  engine_version    = var.rds_engine_version
  instance_class    = var.rds_instance_class
  allocated_storage = var.rds_allocated_storage
  storage_type      = "gp3"
  db_name           = "postgres"
  username          = "postgres"

  # RDS native master user password management via AWS Secrets Manager
  manage_master_user_password = true

  db_subnet_group_name   = aws_db_subnet_group.rds.name
  vpc_security_group_ids = [aws_security_group.rds.id]
  publicly_accessible    = false
  storage_encrypted      = true

  backup_retention_period = 7
  deletion_protection     = var.rds_deletion_protection
  skip_final_snapshot     = var.rds_skip_final_snapshot
  final_snapshot_identifier = var.rds_skip_final_snapshot ? null : (
    "${var.project_name}-${var.environment}-final"
  )

  tags = merge(
    local.common_tags,
    {
      Name = "${var.project_name}-${var.environment}-db"
    }
  )
}
