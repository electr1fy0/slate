locals {
  dashboard_body = jsonencode({
    widgets = [
      {
        type = "metric", x = 0, y = 0, width = 12, height = 6
        properties = {
          title  = "Deep Health (1=healthy)"
          region = var.primary_region
          view   = "timeSeries"
          stat   = "Sum"
          period = 60
          metrics = [
            ["Continuum", "Health", "Role", "primary"],
            ["Continuum", "Health", "Role", "standby", { region = var.primary_region }]
          ]
          yAxis = { left = { min = 0, max = 1 } }
        }
      },
      {
        type = "metric", x = 12, y = 0, width = 12, height = 6
        properties = {
          title  = "Route53 Health Checks"
          region = var.primary_region
          view   = "timeSeries"
          stat   = "Minimum"
          period = 60
          metrics = [
            ["AWS/Route53", "HealthCheckStatus", "HealthCheckId", aws_route53_health_check.primary.id],
            ["AWS/Route53", "HealthCheckStatus", "HealthCheckId", aws_route53_health_check.standby.id]
          ]
          yAxis = { left = { min = 0, max = 1 } }
        }
      },
      {
        type = "metric", x = 0, y = 6, width = 12, height = 6
        properties = {
          title  = "Step Functions Failover Executions"
          region = var.primary_region
          view   = "timeSeries"
          stat   = "Sum"
          period = 60
          metrics = [
            ["AWS/States", "ExecutionsStarted", "StateMachineArn", aws_sfn_state_machine.failover.arn],
            ["AWS/States", "ExecutionsSucceeded", "StateMachineArn", aws_sfn_state_machine.failover.arn],
            ["AWS/States", "ExecutionsFailed", "StateMachineArn", aws_sfn_state_machine.failover.arn],
            ["AWS/States", "ExecutionsAborted", "StateMachineArn", aws_sfn_state_machine.failover.arn]
          ]
        }
      },
      {
        type = "metric", x = 12, y = 6, width = 12, height = 6
        properties = {
          title  = "Reconciliation Activity"
          region = var.primary_region
          view   = "timeSeries"
          stat   = "Sum"
          period = 60
          metrics = [
            ["Continuum", "ReconcilerScanned"],
            ["Continuum", "ReconcilerRepaired"]
          ]
        }
      },
      {
        type       = "alarm", x = 0, y = 12, width = 12, height = 6
        properties = { alarms = [local.primary_alarm_arn], title = "Primary Unhealthy Alarm" }
      },
      {
        type = "text", x = 12, y = 12, width = 12, height = 6
        properties = {
          markdown = "# Continuum\nPrimary: ${local.primary_url}\nStandby: ${local.standby_url}\nFailover workflow: ${aws_sfn_state_machine.failover.name}"
        }
      }
    ]
  })
}

resource "aws_cloudwatch_dashboard" "continuum" {
  dashboard_name = "${var.project}-overview"
  dashboard_body = local.dashboard_body
}
