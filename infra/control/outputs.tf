output "sfn_state_machine_arn" {
  value = aws_sfn_state_machine.failover.arn
}

output "sns_topic_arn" {
  value = aws_sns_topic.alerts.arn
}

output "primary_health_check_id" {
  value = aws_route53_health_check.primary.id
}

output "nameservers" {
  description = "Delegate your domain to these nameservers (if zone created)"
  value       = var.domain_name != "" ? aws_route53_zone.main[0].name_servers : []
}

output "exam_url" {
  description = "Use this if no domain is configured"
  value       = local.primary_url
}

output "failback_state_machine_arn" {
  description = "Manually-triggered failback workflow"
  value       = aws_sfn_state_machine.failback.arn
}
