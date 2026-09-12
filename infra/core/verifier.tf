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
  name               = "${var.project}-verifier-role"
  assume_role_policy = data.aws_iam_policy_document.lambda_assume.json
}

resource "aws_iam_role_policy_attachment" "verifier_logs" {
  role       = aws_iam_role.verifier.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole"
}

resource "aws_iam_role_policy" "verifier_permissions" {
  name = "${var.project}-verifier-permissions"
  role = aws_iam_role.verifier.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect   = "Allow"
        Action   = ["dynamodb:PutItem", "dynamodb:GetItem", "dynamodb:DeleteItem"]
        Resource = [aws_dynamodb_table.continuum.arn, "${aws_dynamodb_table.continuum.arn}/replica/*"]
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
  # scripts/build_go.sh creates these root-level packages. Keep the
  # existence guard on the hash so terraform validate/plan can inspect the
  # stack before a local build; apply still reports a useful missing-file
  # error if deployment is attempted without the package.
  health_verifier_package = "${path.root}/../../build/lambda/health-verifier.zip"
}

resource "aws_lambda_function" "verifier_primary" {
  function_name    = "${var.project}-verifier-primary"
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
      TABLE_NAME       = aws_dynamodb_table.continuum.name
      TARGET_URL       = "http://${module.compute_primary.public_ip}"
      ROLE_LABEL       = "primary"
      METRIC_NAMESPACE = "Continuum"
    }
  }

  depends_on = [aws_iam_role_policy.verifier_permissions, aws_cloudwatch_log_group.verifier]
}

resource "aws_cloudwatch_log_group" "verifier" {
  name              = "/aws/lambda/${var.project}-verifier-primary"
  retention_in_days = 14
}

resource "aws_iam_role" "scheduler" {
  name               = "${var.project}-scheduler-role"
  assume_role_policy = data.aws_iam_policy_document.lambda_assume.json
}

data "aws_iam_policy_document" "scheduler_invoke" {
  statement {
    actions   = ["lambda:InvokeFunction"]
    resources = [aws_lambda_function.verifier_primary.arn]
  }
}

resource "aws_iam_role_policy" "scheduler_invoke" {
  name   = "${var.project}-scheduler-invoke"
  role   = aws_iam_role.scheduler.id
  policy = data.aws_iam_policy_document.scheduler_invoke.json
}

resource "aws_scheduler_schedule" "verify_every_minute" {
  name                         = "${var.project}-deep-health-every-minute"
  schedule_expression          = "rate(1 minute)"
  schedule_expression_timezone = "Asia/Kolkata"

  flexible_time_window {
    mode = "OFF"
  }

  target {
    arn      = aws_lambda_function.verifier_primary.arn
    role_arn = aws_iam_role.scheduler.arn
  }
}

resource "aws_cloudwatch_metric_alarm" "primary_unhealthy" {
  alarm_name          = "${var.project}-primary-unhealthy"
  comparison_operator = "LessThanThreshold"
  evaluation_periods  = 3
  threshold           = 1
  treat_missing_data  = "breaching"

  metric_name = "Health"
  namespace   = "Continuum"
  period      = 60
  statistic   = "Sum"

  dimensions = {
    Role = "primary"
  }

  alarm_description = "Deep health verifier reports primary unhealthy for 3 consecutive minutes. EventBridge rule routes this to the Continuum failover state machine."
}
