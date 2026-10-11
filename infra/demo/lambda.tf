locals {
  environment_keys = {
    router        = ["TABLE_NAME", "BACKEND_A_URL", "BACKEND_B_URL", "UPSTREAM_TIMEOUT_MS"]
    demo-verifier = ["TABLE_NAME", "BACKEND_A_URL", "BACKEND_B_URL", "ADMIN_TOKEN", "PROBE_TIMEOUT_MS"]
    demo-monitor  = ["TABLE_NAME", "VERIFIER_FUNCTION_NAME", "STATE_MACHINE_ARN", "PUBLIC_API_URL", "POLICY_PROBES", "PROBE_INTERVAL_MS", "PROBE_TIMEOUT_MS"]
  }
  shared_environment = {
    TABLE_NAME                 = aws_dynamodb_table.demo.name
    BACKEND_A_URL              = "http://${aws_instance.app["A"].private_ip}:8000"
    BACKEND_B_URL              = "http://${aws_instance.app["B"].private_ip}:8000"
    ADMIN_TOKEN                = random_password.admin.result
    VERIFIER_FUNCTION_NAME     = local.function_names["demo-verifier"]
    CONTROL_FUNCTION_NAME      = local.function_names["demo-control"]
    MONITOR_FUNCTION_NAME      = local.function_names["demo-monitor"]
    RECONCILER_FUNCTION_NAME   = "${var.project}-demo-reconciler"
    STATE_MACHINE_ARN          = local.state_machine_arn
    FAILBACK_STATE_MACHINE_ARN = local.state_machine_arn
    PUBLIC_API_URL             = aws_apigatewayv2_api.demo.api_endpoint
    POLICY_PROBES              = "3"
    PROBE_INTERVAL_MS          = "5000"
    PROBE_TIMEOUT_MS           = "3000"
    UPSTREAM_TIMEOUT_MS        = "8000"
    INSTANCE_ID_A              = aws_instance.app["A"].id
    INSTANCE_ID_B              = aws_instance.app["B"].id
    RUNTIME_ENABLED            = tostring(var.instances_running && var.monitor_enabled)
  }
}
resource "aws_cloudwatch_log_group" "lambda" {
  for_each          = local.function_names
  name              = "/aws/lambda/${each.value}"
  retention_in_days = var.log_retention_days
}
resource "aws_lambda_function" "demo" {
  for_each         = local.function_names
  function_name    = each.value
  role             = aws_iam_role.lambda[each.key].arn
  runtime          = "provided.al2023"
  handler          = "bootstrap"
  architectures    = ["x86_64"]
  filename         = "${path.module}/../../build/demo/${each.key}.zip"
  source_code_hash = fileexists("${path.module}/../../build/demo/${each.key}.zip") ? filebase64sha256("${path.module}/../../build/demo/${each.key}.zip") : null
  memory_size      = 256
  timeout          = each.key == "router" ? 15 : 60
  environment {
    variables = { for key, value in local.shared_environment : key => value if each.key == "demo-control" || contains(lookup(local.environment_keys, each.key, []), key) }
  }
  dynamic "vpc_config" {
    for_each = contains(["router", "demo-verifier"], each.key) ? [1] : []
    content {
      subnet_ids         = [for subnet in aws_subnet.lambda : subnet.id]
      security_group_ids = [aws_security_group.lambda.id]
    }
  }
  depends_on = [aws_cloudwatch_log_group.lambda, aws_iam_role_policy.lambda, aws_iam_role_policy_attachment.logs, aws_iam_role_policy_attachment.vpc, aws_dynamodb_table_item.active]
}
resource "aws_iam_role" "reconciler" {
  name               = "${var.project}-demo-reconciler"
  assume_role_policy = data.aws_iam_policy_document.lambda_trust.json
}
resource "aws_iam_role_policy_attachment" "reconciler_logs" {
  role       = aws_iam_role.reconciler.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole"
}
resource "aws_iam_role_policy" "reconciler" {
  role = aws_iam_role.reconciler.id
  name = "generated-runtime"
  policy = templatefile("${path.module}/policies/demo-reconciler.json.tftpl", {
    table_arn = aws_dynamodb_table.demo.arn
    }
  )
}
resource "aws_cloudwatch_log_group" "reconciler" {
  name              = "/aws/lambda/${var.project}-demo-reconciler"
  retention_in_days = var.log_retention_days
}
resource "aws_lambda_function" "reconciler" {
  function_name    = "${var.project}-demo-reconciler"
  role             = aws_iam_role.reconciler.arn
  runtime          = "provided.al2023"
  handler          = "bootstrap"
  architectures    = ["x86_64"]
  filename         = "${path.module}/../../build/demo/demo-reconciler.zip"
  source_code_hash = fileexists("${path.module}/../../build/demo/demo-reconciler.zip") ? filebase64sha256("${path.module}/../../build/demo/demo-reconciler.zip") : null
  memory_size      = 128
  timeout          = 30
  environment {
    variables = {
      TABLE_NAME = aws_dynamodb_table.demo.name
    }
  }
  depends_on = [aws_iam_role_policy.reconciler, aws_iam_role_policy_attachment.reconciler_logs, aws_cloudwatch_log_group.reconciler]
}
resource "aws_apigatewayv2_api" "demo" {
  name          = var.project
  protocol_type = "HTTP"
  cors_configuration {
    allow_origins  = ["https://${aws_cloudfront_distribution.frontend.domain_name}", "http://localhost:3000"]
    allow_methods  = ["GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"]
    allow_headers  = ["authorization", "content-type", "x-admin-token"]
    expose_headers = ["X-Continuum-Backend", "X-Continuum-Epoch"]
    max_age        = 300
  }
}
resource "aws_apigatewayv2_integration" "demo" {
  for_each               = toset(["router", "demo-control"])
  api_id                 = aws_apigatewayv2_api.demo.id
  integration_type       = "AWS_PROXY"
  integration_uri        = aws_lambda_function.demo[each.key].invoke_arn
  payload_format_version = "2.0"
  timeout_milliseconds   = 15000
}
resource "aws_apigatewayv2_route" "default" {
  api_id    = aws_apigatewayv2_api.demo.id
  route_key = "$default"
  target    = "integrations/${aws_apigatewayv2_integration.demo["router"].id}"
}
resource "aws_apigatewayv2_route" "control" {
  for_each  = toset(["GET /continuum/status", "POST /continuum/action"])
  api_id    = aws_apigatewayv2_api.demo.id
  route_key = each.key
  target    = "integrations/${aws_apigatewayv2_integration.demo["demo-control"].id}"
}
resource "aws_apigatewayv2_stage" "demo" {
  api_id      = aws_apigatewayv2_api.demo.id
  name        = "$default"
  auto_deploy = true
  default_route_settings {
    throttling_burst_limit = 6
    throttling_rate_limit  = 3
  }
}
resource "aws_lambda_permission" "api" {
  for_each      = toset(["router", "demo-control"])
  statement_id  = "AllowDemoHTTPAPI"
  action        = "lambda:InvokeFunction"
  function_name = local.function_names[each.key]
  principal     = "apigateway.amazonaws.com"
  source_arn    = "${aws_apigatewayv2_api.demo.execution_arn}/*/*"
  depends_on    = [aws_lambda_function.demo]
}
