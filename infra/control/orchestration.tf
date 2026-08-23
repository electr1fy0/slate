resource "aws_sns_topic" "alerts" {
  name = "${var.project}-alerts"
}

resource "aws_sns_topic_subscription" "email" {
  topic_arn = aws_sns_topic.alerts.arn
  protocol  = "email"
  endpoint  = var.alert_email
}

locals {
  state_machine_definition = templatefile("${path.root}/../../statemachine/failover.asl.json", {
    VERIFIER_ARN   = aws_lambda_function.verifier_sfn.arn
    OPS_ARN        = aws_lambda_function.ops.arn
    RECONCILER_ARN = aws_lambda_function.reconciler.arn
    SNS_TOPIC_ARN  = aws_sns_topic.alerts.arn
    STANDBY_URL    = local.standby_url
  })

  failback_definition = templatefile("${path.root}/../../statemachine/failback.asl.json", {
    VERIFIER_ARN   = aws_lambda_function.verifier_sfn.arn
    OPS_ARN        = aws_lambda_function.ops.arn
    RECONCILER_ARN = aws_lambda_function.reconciler.arn
    SNS_TOPIC_ARN  = aws_sns_topic.alerts.arn
    PRIMARY_URL    = local.primary_url
  })
}

resource "aws_iam_role" "state_machine" {
  name               = "${var.project}-sfn-role"
  assume_role_policy = data.aws_iam_policy_document.lambda_assume.json
}

data "aws_iam_policy_document" "sfn_permissions" {
  statement {
    actions   = ["lambda:InvokeFunction"]
    resources = [aws_lambda_function.verifier_sfn.arn, aws_lambda_function.ops.arn, aws_lambda_function.reconciler.arn]
  }
  statement {
    actions   = ["sns:Publish"]
    resources = [aws_sns_topic.alerts.arn]
  }
}

resource "aws_iam_role_policy" "sfn_permissions" {
  name   = "${var.project}-sfn-permissions"
  role   = aws_iam_role.state_machine.id
  policy = data.aws_iam_policy_document.sfn_permissions.json
}

resource "aws_sfn_state_machine" "failover" {
  name     = "${var.project}-failover"
  role_arn = aws_iam_role.state_machine.arn

  definition = local.state_machine_definition

  logging_configuration {
    log_destination        = "${aws_cloudwatch_log_group.sfn.arn}:*"
    include_execution_data = true
    level                  = "ERROR"
  }

  depends_on = [aws_iam_role_policy.sfn_permissions]
}

resource "aws_cloudwatch_log_group" "sfn" {
  name              = "/aws/states/${var.project}-failover"
  retention_in_days = 14
}

# Failback is deliberately manual-only: an operator starts it once the
# primary region is confirmed healthy. There is no automatic trigger.
resource "aws_sfn_state_machine" "failback" {
  name     = "${var.project}-failback"
  role_arn = aws_iam_role.state_machine.arn

  definition = local.failback_definition

  logging_configuration {
    log_destination        = "${aws_cloudwatch_log_group.sfn_failback.arn}:*"
    include_execution_data = true
    level                  = "ERROR"
  }

  depends_on = [aws_iam_role_policy.sfn_permissions]
}

resource "aws_cloudwatch_log_group" "sfn_failback" {
  name              = "/aws/states/${var.project}-failback"
  retention_in_days = 14
}

resource "aws_iam_role" "eventbridge_trigger" {
  name               = "${var.project}-eb-trigger-role"
  assume_role_policy = data.aws_iam_policy_document.lambda_assume.json
}

data "aws_iam_policy_document" "trigger_assume" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["events.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "alarm_to_sfn" {
  name               = "${var.project}-alarm-to-sfn-role"
  assume_role_policy = data.aws_iam_policy_document.trigger_assume.json
}

resource "aws_iam_role_policy" "alarm_to_sfn" {
  name = "${var.project}-alarm-to-sfn-policy"
  role = aws_iam_role.alarm_to_sfn.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect   = "Allow"
        Action   = ["states:StartExecution"]
        Resource = [aws_sfn_state_machine.failover.arn]
      }
    ]
  })
}

resource "aws_cloudwatch_event_rule" "primary_alarm" {
  name        = "${var.project}-primary-alarm-to-sfn"
  description = "Start the Continuum failover workflow when the primary deep-health alarm fires"

  event_pattern = jsonencode({
    source      = ["aws.cloudwatch"]
    detail-type = ["CloudWatch Alarm State Change"]
    resources   = [local.primary_alarm_arn]
    detail = {
      state = { value = ["ALARM"] }
    }
  })
}

resource "aws_cloudwatch_event_target" "start_failover" {
  rule     = aws_cloudwatch_event_rule.primary_alarm.name
  arn      = aws_sfn_state_machine.failover.arn
  role_arn = aws_iam_role.alarm_to_sfn.arn
}
