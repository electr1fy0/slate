resource "aws_ssm_parameter" "standby_instance_id" {
  provider = aws.primary
  name     = "/continuum/standby/instance_id"
  type     = "String"
  value    = module.compute_standby.instance_id
}

resource "aws_ssm_parameter" "standby_public_ip" {
  provider = aws.primary
  name     = "/continuum/standby/public_ip"
  type     = "String"
  value    = module.compute_standby.public_ip
}

resource "aws_ssm_parameter" "standby_alarm_name" {
  provider = aws.primary
  name     = "/continuum/alarm/standby_name"
  type     = "String"
  value    = aws_cloudwatch_metric_alarm.standby_unhealthy.alarm_name
}
