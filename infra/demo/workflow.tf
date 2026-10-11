resource "aws_sfn_state_machine" "failover" {
  name       = "${var.project}-failover"
  role_arn   = aws_iam_role.state_machine.arn
  type       = "STANDARD"
  definition = replace(replace(replace(replace(file("${path.module}/../../statemachine/demo-failover.asl.json"), "__CONTROL_ARN__", local.function_arns["demo-control"]), "__VERIFIER_ARN__", local.function_arns["demo-verifier"]), "__RECONCILER_ARN__", aws_lambda_function.reconciler.arn), "__PUBLIC_API_URL__", aws_apigatewayv2_api.demo.api_endpoint)
  depends_on = [aws_iam_role_policy.state_machine]
}
resource "aws_cloudwatch_event_rule" "monitor" {
  name                = "${var.project}-monitor"
  schedule_expression = "rate(1 minute)"
  state               = var.monitor_enabled ? "ENABLED" : "DISABLED"
}
resource "aws_cloudwatch_event_target" "monitor" {
  rule       = aws_cloudwatch_event_rule.monitor.name
  arn        = local.function_arns["demo-monitor"]
  depends_on = [aws_lambda_function.demo, aws_lambda_permission.monitor]
}
resource "aws_lambda_permission" "monitor" {
  statement_id  = "AllowBoundedDemoMonitor"
  action        = "lambda:InvokeFunction"
  function_name = local.function_names["demo-monitor"]
  principal     = "events.amazonaws.com"
  source_arn    = aws_cloudwatch_event_rule.monitor.arn
  depends_on    = [aws_lambda_function.demo]
}
