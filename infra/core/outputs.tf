output "primary_instance_id" {
  value = module.compute_primary.instance_id
}

output "primary_public_ip" {
  value = module.compute_primary.public_ip
}

output "table_name" {
  value = aws_dynamodb_table.continuum.name
}

output "bundle_bucket_primary" {
  value = aws_s3_bucket.bundle_primary.id
}

output "verifier_lambda_arn" {
  value = aws_lambda_function.verifier_primary.arn
}

output "alarm_name" {
  value = aws_cloudwatch_metric_alarm.primary_unhealthy.alarm_name
}

output "app_url" {
  value = "http://${module.compute_primary.public_ip}"
}
