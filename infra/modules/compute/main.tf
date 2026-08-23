data "aws_ami" "al2023" {
  most_recent = true
  owners      = ["amazon"]

  filter {
    name   = "name"
    values = ["al2023-ami-2023.*-x86_64"]
  }
}

data "aws_iam_policy_document" "ec2_assume" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["ec2.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "app" {
  name               = "${var.project}-${var.region_label}-app-role"
  assume_role_policy = data.aws_iam_policy_document.ec2_assume.json
}

resource "aws_iam_role_policy_attachment" "ssm_core" {
  role       = aws_iam_role.app.name
  policy_arn = "arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"
}

resource "aws_iam_role_policy" "app_permissions" {
  name = "${var.project}-${var.region_label}-app-permissions"
  role = aws_iam_role.app.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid      = "DynamoTableAccess"
        Effect   = "Allow"
        Action   = ["dynamodb:GetItem", "dynamodb:PutItem", "dynamodb:DeleteItem", "dynamodb:Query", "dynamodb:Scan", "dynamodb:UpdateItem", "dynamodb:ConditionCheckItem"]
        Resource = "arn:aws:dynamodb:*:*:table/${var.table_name}"
      },
      {
        Sid      = "ReadAppBundle"
        Effect   = "Allow"
        Action   = ["s3:GetObject", "s3:ListBucket"]
        Resource = ["arn:aws:s3:::${var.bundle_bucket}", "arn:aws:s3:::${var.bundle_bucket}/*"]
      },
      {
        Sid      = "PublishHealthMetrics"
        Effect   = "Allow"
        Action   = ["cloudwatch:PutMetricData"]
        Resource = "*"
        Condition = {
          StringEquals = { "cloudwatch:namespace" = "Continuum" }
        }
      }
    ]
  })
}

resource "aws_iam_instance_profile" "app" {
  name = "${var.project}-${var.region_label}-app-profile"
  role = aws_iam_role.app.name
}

locals {
  cloud_config = templatefile("${path.module}/templates/user-data.sh.tftpl", {
    bundle_bucket = var.bundle_bucket
    app_role      = var.app_role
    table_name    = var.table_name
    region        = data.aws_region.current.name
  })
}

data "aws_region" "current" {}

resource "aws_instance" "app" {
  ami                         = data.aws_ami.al2023.id
  instance_type               = var.instance_type
  subnet_id                   = var.subnet_id
  vpc_security_group_ids      = [var.security_group_id]
  iam_instance_profile        = aws_iam_instance_profile.app.name
  user_data                   = local.cloud_config
  user_data_replace_on_change = false

  root_block_device {
    volume_size = 8
    volume_type = "gp3"
  }

  metadata_options {
    http_tokens = "required"
  }

  tags = {
    Name      = "${var.project}-${var.region_label}-app"
    continuum = "true"
    Role      = var.app_role
    Project   = var.project
  }

  lifecycle {
    ignore_changes = [ami, user_data]
  }
}

resource "aws_eip" "app" {
  domain     = "vpc"
  instance   = aws_instance.app.id
  depends_on = []

  tags = { Name = "${var.project}-${var.region_label}-eip" }
}
