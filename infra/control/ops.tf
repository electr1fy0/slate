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
        Action   = ["ssm:DescribeDocument", "ssm:GetDocument", "ssm:ListCommandInvocations", "ssm:GetCommandInvocation"]
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

resource "aws_cloudwatch_log_group" "ops" {
  name              = "/aws/lambda/${var.project}-failover-ops"
  retention_in_days = 14
}

resource "aws_lambda_function" "ops" {
  function_name    = "${var.project}-failover-ops"
  role             = aws_iam_role.ops.arn
  runtime          = "provided.al2023"
  handler          = "bootstrap"
  architectures    = ["x86_64"]
  filename         = "${path.root}/../../build/lambda/failover-ops.zip"
  source_code_hash = fileexists("${path.root}/../../build/lambda/failover-ops.zip") ? filebase64sha256("${path.root}/../../build/lambda/failover-ops.zip") : null
  # Promote/demote now poll the SSM command to completion (up to ~3 min).
  timeout = 300

  environment {
    variables = {
      STANDBY_INSTANCE_ID     = data.aws_ssm_parameter.standby_instance_id.value
      PRIMARY_HEALTH_CHECK_ID = aws_route53_health_check.primary.id
      TABLE_NAME              = data.aws_ssm_parameter.table_name.value
      SOURCE_REGION           = var.primary_region
      STANDBY_REGION          = var.standby_region
      COMMAND_WAIT_TIMEOUT_S  = "180"
    }
  }

  depends_on = [aws_iam_role_policy.ops_permissions, aws_cloudwatch_log_group.ops]
}

# Async failures land in a DLQ instead of vanishing.
resource "aws_lambda_function_event_invoke_config" "ops" {
  function_name                = aws_lambda_function.ops.function_name
  maximum_retry_attempts       = 1
  maximum_event_age_in_seconds = 3600

  destination_config {
    on_failure {
      destination = aws_sqs_queue.dlq["ops"].arn
    }
  }
}

resource "aws_lambda_function_event_invoke_config" "reconciler" {
  function_name                = aws_lambda_function.reconciler.function_name
  maximum_retry_attempts       = 1
  maximum_event_age_in_seconds = 3600

  destination_config {
    on_failure {
      destination = aws_sqs_queue.dlq["reconciler"].arn
    }
  }
}

resource "aws_lambda_function_event_invoke_config" "verifier_sfn" {
  function_name                = aws_lambda_function.verifier_sfn.function_name
  maximum_retry_attempts       = 1
  maximum_event_age_in_seconds = 3600

  destination_config {
    on_failure {
      destination = aws_sqs_queue.dlq["verifier"].arn
    }
  }
}
