# Dead-letter queues catch async Lambda failures so nothing disappears
# silently; a scheduled reconciler replays the outbox between failovers;
# alarms page when either mechanism has something to say.

locals {
  dlq_members = ["verifier", "ops", "reconciler"]
}

resource "aws_sqs_queue" "dlq" {
  for_each                  = toset(local.dlq_members)
  name                      = "${var.project}-${each.key}-dlq"
  message_retention_seconds = 14 * 24 * 3600
}

# The execution role needs sqs:SendMessage for onFailure destinations.
resource "aws_iam_role_policy" "dlq_send" {
  for_each = toset(local.dlq_members)
  name     = "${var.project}-${each.key}-dlq-send"
  role = each.key == "verifier" ? aws_iam_role.verifier_sfn.id : (
    each.key == "ops" ? aws_iam_role.ops.id : aws_iam_role.reconciler.id
  )
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = ["sqs:SendMessage"]
      Resource = [aws_sqs_queue.dlq[each.key].arn]
    }]
  })
}

# ---------------------------------------------------------------- schedules

# Reconciliation must not depend on a failover happening: drift between
# failovers is replayed on this cadence instead.
resource "aws_cloudwatch_event_rule" "reconciler_schedule" {
  name                = "${var.project}-reconciler-schedule"
  description         = "Periodically replay any in-flight writes left in the journal"
  schedule_expression = "rate(${var.reconciler_schedule_minutes} minutes)"
}

data "aws_iam_policy_document" "events_invoke_assume" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["events.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "events_invoke_reconciler" {
  name               = "${var.project}-events-invoke-reconciler-role"
  assume_role_policy = data.aws_iam_policy_document.events_invoke_assume.json
}

resource "aws_iam_role_policy" "events_invoke_reconciler" {
  name = "${var.project}-events-invoke-reconciler-policy"
  role = aws_iam_role.events_invoke_reconciler.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = ["lambda:InvokeFunction"]
      Resource = [aws_lambda_function.reconciler.arn]
    }]
  })
}

resource "aws_cloudwatch_event_target" "reconciler_schedule" {
  rule     = aws_cloudwatch_event_rule.reconciler_schedule.name
  arn      = aws_lambda_function.reconciler.arn
  role_arn = aws_iam_role.events_invoke_reconciler.arn
}

resource "aws_lambda_permission" "reconciler_from_schedule" {
  statement_id  = "AllowExecutionFromEventBridgeSchedule"
  action        = "lambda:InvokeFunction"
  function_name = aws_lambda_function.reconciler.function_name
  principal     = "events.amazonaws.com"
  source_arn    = aws_cloudwatch_event_rule.reconciler_schedule.arn
}

# ------------------------------------------------------------------- alarms

resource "aws_cloudwatch_metric_alarm" "dlq_not_empty" {
  for_each            = toset(local.dlq_members)
  alarm_name          = "${var.project}-dlq-not-empty-${each.key}"
  alarm_description   = "Messages landed in the ${each.key} dead-letter queue; async work was lost."
  namespace           = "AWS/SQS"
  metric_name         = "ApproximateNumberOfMessagesVisible"
  dimensions          = { QueueName = aws_sqs_queue.dlq[each.key].name }
  statistic           = "Sum"
  period              = 300
  evaluation_periods  = 1
  threshold           = 0
  comparison_operator = "GreaterThanThreshold"
  treat_missing_data  = "notBreaching"
  alarm_actions       = [aws_sns_topic.alerts.arn]
  ok_actions          = [aws_sns_topic.alerts.arn]
}

resource "aws_cloudwatch_metric_alarm" "failover_executions_failed" {
  alarm_name          = "${var.project}-failover-executions-failed"
  alarm_description   = "A failover or failback execution ended in a Failed state."
  namespace           = "AWS/States"
  metric_name         = "ExecutionsFailed"
  dimensions          = { StateMachineArn = aws_sfn_state_machine.failover.arn }
  statistic           = "Sum"
  period              = 300
  evaluation_periods  = 1
  threshold           = 1
  comparison_operator = "GreaterThanOrEqualToThreshold"
  treat_missing_data  = "notBreaching"
  alarm_actions       = [aws_sns_topic.alerts.arn]
}

resource "aws_cloudwatch_metric_alarm" "failback_executions_failed" {
  alarm_name          = "${var.project}-failback-executions-failed"
  alarm_description   = "A manually-triggered failback execution ended in a Failed state."
  namespace           = "AWS/States"
  metric_name         = "ExecutionsFailed"
  dimensions          = { StateMachineArn = aws_sfn_state_machine.failback.arn }
  statistic           = "Sum"
  period              = 300
  evaluation_periods  = 1
  threshold           = 1
  comparison_operator = "GreaterThanOrEqualToThreshold"
  treat_missing_data  = "notBreaching"
  alarm_actions       = [aws_sns_topic.alerts.arn]
}
