resource "aws_ssm_parameter" "table_name" {
  name  = "/continuum/table/name"
  type  = "String"
  value = aws_dynamodb_table.continuum.name
}

resource "aws_ssm_parameter" "bundle_bucket_primary" {
  name  = "/continuum/bucket/bundle_primary"
  type  = "String"
  value = aws_s3_bucket.bundle_primary.id
}

resource "aws_ssm_parameter" "assets_bucket_primary" {
  name  = "/continuum/bucket/assets_primary"
  type  = "String"
  value = aws_s3_bucket.assets_primary.id
}

resource "aws_ssm_parameter" "primary_instance_id" {
  name  = "/continuum/primary/instance_id"
  type  = "String"
  value = module.compute_primary.instance_id
}

resource "aws_ssm_parameter" "primary_public_ip" {
  name  = "/continuum/primary/public_ip"
  type  = "String"
  value = module.compute_primary.public_ip
}

resource "aws_ssm_parameter" "primary_alarm_name" {
  name  = "/continuum/alarm/primary_name"
  type  = "String"
  value = aws_cloudwatch_metric_alarm.primary_unhealthy.alarm_name
}
