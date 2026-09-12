data "aws_ssm_parameter" "table_name" {
  provider = aws.primary
  name     = "/continuum/table/name"
}

data "aws_ssm_parameter" "bundle_bucket_standby" {
  provider = aws.primary
  name     = "/continuum/bucket/bundle_standby"
}

module "network_standby" {
  source            = "../modules/network"
  project           = var.project
  region_label      = "standby"
  availability_zone = var.standby_az
  admin_cidrs       = var.admin_cidrs
}

module "compute_standby" {
  source            = "../modules/compute"
  project           = var.project
  region_label      = "standby"
  instance_type     = var.instance_type
  subnet_id         = module.network_standby.subnet_id
  security_group_id = module.network_standby.security_group_id
  app_role          = "standby"
  table_name        = data.aws_ssm_parameter.table_name.value
  bundle_bucket     = data.aws_ssm_parameter.bundle_bucket_standby.value
}

data "aws_iam_policy_document" "lambda_assume" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["lambda.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "verifier" {
  name               = "${var.project}-verifier-role-standby"
  assume_role_policy = data.aws_iam_policy_document.lambda_assume.json
}

resource "aws_iam_role_policy_attachment" "verifier_logs" {
  role       = aws_iam_role.verifier.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole"
}

resource "aws_iam_role_policy" "verifier_permissions" {
  name = "${var.project}-verifier-permissions-standby"
  role = aws_iam_role.verifier.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect   = "Allow"
        Action   = ["dynamodb:PutItem", "dynamodb:GetItem", "dynamodb:DeleteItem"]
        Resource = ["arn:aws:dynamodb:*:*:table/${data.aws_ssm_parameter.table_name.value}"]
      },
      {
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

locals {
  health_verifier_package = "${path.root}/../../build/lambda/health-verifier.zip"
}

resource "aws_cloudwatch_log_group" "verifier" {
  name              = "/aws/lambda/${var.project}-verifier-standby"
  retention_in_days = 14
}

resource "aws_lambda_function" "verifier_standby" {
  function_name    = "${var.project}-verifier-standby"
  role             = aws_iam_role.verifier.arn
  runtime          = "provided.al2023"
  handler          = "bootstrap"
  architectures    = ["x86_64"]
  filename         = local.health_verifier_package
  source_code_hash = fileexists(local.health_verifier_package) ? filebase64sha256(local.health_verifier_package) : null
  timeout          = 60
  memory_size      = 128

  environment {
    variables = {
      TABLE_NAME       = data.aws_ssm_parameter.table_name.value
      TARGET_URL       = "http://${module.compute_standby.public_ip}"
      ROLE_LABEL       = "standby"
      METRIC_NAMESPACE = "Continuum"
    }
  }

  depends_on = [aws_iam_role_policy.verifier_permissions, aws_cloudwatch_log_group.verifier]
}

resource "aws_iam_role" "scheduler" {
  name               = "${var.project}-scheduler-role-standby"
  assume_role_policy = data.aws_iam_policy_document.lambda_assume.json
}

resource "aws_iam_role_policy" "scheduler_invoke" {
  name = "${var.project}-scheduler-invoke-standby"
  role = aws_iam_role.scheduler.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect   = "Allow"
        Action   = ["lambda:InvokeFunction"]
        Resource = [aws_lambda_function.verifier_standby.arn]
      }
    ]
  })
}

resource "aws_scheduler_schedule" "verify_every_minute_standby" {
  name                         = "${var.project}-deep-health-every-minute-standby"
  schedule_expression          = "rate(1 minute)"
  schedule_expression_timezone = "Asia/Singapore"

  flexible_time_window {
    mode = "OFF"
  }

  target {
    arn      = aws_lambda_function.verifier_standby.arn
    role_arn = aws_iam_role.scheduler.arn
  }
}

resource "aws_cloudwatch_metric_alarm" "standby_unhealthy" {
  alarm_name          = "${var.project}-standby-unhealthy"
  comparison_operator = "LessThanThreshold"
  evaluation_periods  = 3
  threshold           = 1
  treat_missing_data  = "breaching"

  metric_name = "Health"
  namespace   = "Continuum"
  period      = 60
  statistic   = "Sum"

  dimensions = {
    Role = "standby"
  }

  alarm_description = "Standby deep health failing. No automated failover is triggered from this alarm; it exists for observability."
}
