data "aws_iam_policy_document" "host_assume" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["ec2.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "host" {
  name               = "${var.project_name}-ci-runner-host"
  assume_role_policy = data.aws_iam_policy_document.host_assume.json
  tags               = local.common_tags
}

data "aws_iam_policy_document" "host" {
  statement {
    sid = "SSMControlChannel"
    actions = [
      "ssm:UpdateInstanceInformation",
      "ssmmessages:CreateControlChannel",
      "ssmmessages:CreateDataChannel",
      "ssmmessages:OpenControlChannel",
      "ssmmessages:OpenDataChannel"
    ]
    resources = ["*"]
  }
}

resource "aws_iam_role_policy" "host" {
  name   = "ci-host-bootstrap"
  role   = aws_iam_role.host.id
  policy = data.aws_iam_policy_document.host.json
}

resource "aws_iam_instance_profile" "host" {
  name = "${var.project_name}-ci-runner-host"
  role = aws_iam_role.host.name
  tags = local.common_tags
}

data "aws_iam_openid_connect_provider" "github" {
  arn = var.github_oidc_provider_arn
}

data "aws_iam_policy_document" "build_assume" {
  statement {
    actions = ["sts:AssumeRoleWithWebIdentity"]
    principals {
      type        = "Federated"
      identifiers = [data.aws_iam_openid_connect_provider.github.arn]
    }
    condition {
      test     = "StringEquals"
      variable = "token.actions.githubusercontent.com:aud"
      values   = ["sts.amazonaws.com"]
    }
    condition {
      test     = "StringLike"
      variable = "token.actions.githubusercontent.com:sub"
      values   = ["repo:${var.github_repository}:environment:ci-build"]
    }
  }
}

resource "aws_iam_role" "build" {
  name               = "${var.project_name}-ci-candidate-build"
  assume_role_policy = data.aws_iam_policy_document.build_assume.json
  tags               = local.common_tags
}

data "aws_iam_policy_document" "build" {
  statement {
    sid       = "ECRLogin"
    actions   = ["ecr:GetAuthorizationToken"]
    resources = ["*"]
  }
  statement {
    sid = "CandidateRepositoriesOnly"
    actions = [
      "ecr:BatchCheckLayerAvailability",
      "ecr:BatchGetImage",
      "ecr:CompleteLayerUpload",
      "ecr:GetDownloadUrlForLayer",
      "ecr:InitiateLayerUpload",
      "ecr:DescribeRepositories",
      "ecr:DescribeImages",
      "ecr:ListImages",
      "ecr:PutImage",
      "ecr:UploadLayerPart"
    ]
    resources = [
      "arn:aws:ecr:${var.aws_region}:${var.expected_aws_account_id}:repository/${var.candidate_repository_prefix}*"
    ]
  }
}

resource "aws_iam_role_policy" "build" {
  name   = "candidate-ecr-only"
  role   = aws_iam_role.build.id
  policy = data.aws_iam_policy_document.build.json
}
