resource "aws_iam_role" "reconciler" {
  name               = "${var.project}-reconciler-role"
  assume_role_policy = data.aws_iam_policy_document.lambda_assume.json
}

resource "aws_iam_role_policy_attachment" "reconciler_logs" {
  role       = aws_iam_role.reconciler.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole"
}

resource "aws_iam_role_policy" "reconciler_permissions" {
  name = "${var.project}-reconciler-permissions"
  role = aws_iam_role.reconciler.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect   = "Allow"
        Action   = ["dynamodb:GetItem", "dynamodb:PutItem", "dynamodb:Scan", "dynamodb:Query", "dynamodb:ConditionCheckItem"]
        Resource = ["arn:aws:dynamodb:*:${data.aws_caller_identity.current.account_id}:table/${data.aws_ssm_parameter.table_name.value}"]
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

data "archive_file" "reconciler" {
  type        = "zip"
  source_dir  = "${path.root}/../../lambdas/reconciler"
  output_path = "${path.root}/build/reconciler.zip"
}

resource "aws_cloudwatch_log_group" "reconciler" {
  name              = "/aws/lambda/${var.project}-reconciler"
  retention_in_days = 14
}

resource "aws_lambda_function" "reconciler" {
  function_name    = "${var.project}-reconciler"
  role             = aws_iam_role.reconciler.arn
  runtime          = "python3.12"
  handler          = "handler.handler"
  filename         = data.archive_file.reconciler.output_path
  source_code_hash = data.archive_file.reconciler.output_base64sha256
  timeout          = 120

  environment {
    variables = {
      TABLE_NAME       = data.aws_ssm_parameter.table_name.value
      LOOKBACK_MINUTES = tostring(var.reconciler_lookback_minutes)
      METRIC_NAMESPACE = "Continuum"
    }
  }

  depends_on = [aws_iam_role_policy.reconciler_permissions, aws_cloudwatch_log_group.reconciler]
}

data "archive_file" "verifier_sfn" {
  type        = "zip"
  source_dir  = "${path.root}/../../lambdas/health_verifier"
  output_path = "${path.root}/build/health_verifier_sfn.zip"
}

resource "aws_iam_role" "verifier_sfn" {
  name               = "${var.project}-verifier-sfn-role"
  assume_role_policy = data.aws_iam_policy_document.lambda_assume.json
}

resource "aws_iam_role_policy_attachment" "verifier_sfn_logs" {
  role       = aws_iam_role.verifier_sfn.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole"
}

resource "aws_iam_role_policy" "verifier_sfn_permissions" {
  name = "${var.project}-verifier-sfn-permissions"
  role = aws_iam_role.verifier_sfn.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect   = "Allow"
        Action   = ["dynamodb:PutItem", "dynamodb:GetItem", "dynamodb:DeleteItem"]
        Resource = ["arn:aws:dynamodb:*:${data.aws_caller_identity.current.account_id}:table/${data.aws_ssm_parameter.table_name.value}"]
      }
    ]
  })
}

resource "aws_cloudwatch_log_group" "verifier_sfn" {
  name              = "/aws/lambda/${var.project}-verifier-sfn"
  retention_in_days = 14
}

resource "aws_lambda_function" "verifier_sfn" {
  function_name    = "${var.project}-verifier-sfn"
  role             = aws_iam_role.verifier_sfn.arn
  runtime          = "python3.12"
  handler          = "handler.handler"
  filename         = data.archive_file.verifier_sfn.output_path
  source_code_hash = data.archive_file.verifier_sfn.output_base64sha256
  timeout          = 60

  environment {
    variables = {
      TABLE_NAME       = data.aws_ssm_parameter.table_name.value
      TARGET_URL       = local.primary_url
      ROLE_LABEL       = "sfn-checker"
      METRIC_NAMESPACE = "Continuum"
    }
  }

  depends_on = [aws_iam_role_policy.verifier_sfn_permissions, aws_cloudwatch_log_group.verifier_sfn]
}
