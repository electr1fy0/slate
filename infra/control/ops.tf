data "aws_iam_policy_document" "lambda_assume" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["lambda.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "ops" {
  name               = "${var.project}-failover-ops-role"
  assume_role_policy = data.aws_iam_policy_document.lambda_assume.json
}

resource "aws_iam_role_policy_attachment" "ops_logs" {
  role       = aws_iam_role.ops.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole"
}

resource "aws_iam_role_policy" "ops_permissions" {
  name = "${var.project}-failover-ops-permissions"
  role = aws_iam_role.ops.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "SsmRunCommandOnStandby"
        Effect = "Allow"
        Action = ["ssm:SendCommand"]
        Resource = [
          "arn:aws:ssm:*::document/AWS-RunShellScript",
          "arn:aws:ec2:*:${data.aws_caller_identity.current.account_id}:instance/${data.aws_ssm_parameter.standby_instance_id.value}"
        ]
      },
      {
        Sid      = "SsmDocuments"
        Effect   = "Allow"
        Action   = ["ssm:DescribeDocument", "ssm:GetDocument", "ssm:ListCommandInvocations"]
        Resource = ["arn:aws:ssm:*::document/AWS-RunShellScript", "arn:aws:ssm:*:${data.aws_caller_identity.current.account_id}:*"]
      },
      {
        Sid      = "Route53InvertHealthCheck"
        Effect   = "Allow"
        Action   = ["route53:UpdateHealthCheck", "route53:GetHealthCheck"]
        Resource = ["arn:aws:route53:::healthcheck/${aws_route53_health_check.primary.id}"]
      },
      {
        Sid      = "DdbReplicationProbe"
        Effect   = "Allow"
        Action   = ["dynamodb:PutItem", "dynamodb:GetItem", "dynamodb:DeleteItem", "dynamodb:Query"]
        Resource = ["arn:aws:dynamodb:*:${data.aws_caller_identity.current.account_id}:table/${data.aws_ssm_parameter.table_name.value}"]
      }
    ]
  })
}

data "archive_file" "ops" {
  type        = "zip"
  source_dir  = "${path.root}/../../lambdas/failover_ops"
  output_path = "${path.root}/build/failover_ops.zip"
}

resource "aws_cloudwatch_log_group" "ops" {
  name              = "/aws/lambda/${var.project}-failover-ops"
  retention_in_days = 14
}

resource "aws_lambda_function" "ops" {
  function_name    = "${var.project}-failover-ops"
  role             = aws_iam_role.ops.arn
  runtime          = "python3.12"
  handler          = "handler.handler"
  filename         = data.archive_file.ops.output_path
  source_code_hash = data.archive_file.ops.output_base64sha256
  timeout          = 120

  environment {
    variables = {
      STANDBY_INSTANCE_ID     = data.aws_ssm_parameter.standby_instance_id.value
      PRIMARY_HEALTH_CHECK_ID = aws_route53_health_check.primary.id
      TABLE_NAME              = data.aws_ssm_parameter.table_name.value
      SOURCE_REGION           = var.primary_region
      STANDBY_REGION          = var.standby_region
    }
  }

  depends_on = [aws_iam_role_policy.ops_permissions, aws_cloudwatch_log_group.ops]
}
