output "region" {
  value = var.region
}
output "api_url" {
  value = aws_apigatewayv2_api.demo.api_endpoint
}
output "frontend_url" {
  value = "https://${aws_cloudfront_distribution.frontend.domain_name}"
}
output "frontend_bucket" {
  value = aws_s3_bucket.frontend.id
}
output "bundle_bucket" {
  value = aws_s3_bucket.bundle.id
}
output "distribution_id" {
  value = aws_cloudfront_distribution.frontend.id
}
output "instance_ids" {
  value = {
    for k, v in aws_instance.app : k => v.id
  }
}
output "state_machine_arn" {
  value = aws_sfn_state_machine.failover.arn
}
output "monitor_rule" {
  value = aws_cloudwatch_event_rule.monitor.name
}
output "runtime_parameter" {
  value = aws_ssm_parameter.runtime.name
}
output "admin_token" {
  value     = random_password.admin.result
  sensitive = true
}
output "teacher_email" {
  value = var.teacher_email
}
output "teacher_password" {
  value     = random_password.teacher.result
  sensitive = true
}
