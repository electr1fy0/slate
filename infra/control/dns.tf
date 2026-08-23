data "aws_caller_identity" "current" {}

data "aws_ssm_parameter" "table_name" {
  name = "/continuum/table/name"
}

data "aws_ssm_parameter" "primary_instance_id" {
  name = "/continuum/primary/instance_id"
}

data "aws_ssm_parameter" "primary_public_ip" {
  name = "/continuum/primary/public_ip"
}

data "aws_ssm_parameter" "standby_instance_id" {
  name = "/continuum/standby/instance_id"
}

data "aws_ssm_parameter" "standby_public_ip" {
  name = "/continuum/standby/public_ip"
}

data "aws_ssm_parameter" "primary_alarm_name" {
  name = "/continuum/alarm/primary_name"
}

data "aws_ssm_parameter" "standby_alarm_name" {
  name = "/continuum/alarm/standby_name"
}

locals {
  primary_alarm_arn = "arn:aws:cloudwatch:${var.primary_region}:${data.aws_caller_identity.current.account_id}:alarm:${data.aws_ssm_parameter.primary_alarm_name.value}"
  primary_url       = "http://${data.aws_ssm_parameter.primary_public_ip.value}"
  standby_url       = "http://${data.aws_ssm_parameter.standby_public_ip.value}"
}

resource "aws_route53_health_check" "primary" {
  ip_address        = data.aws_ssm_parameter.primary_public_ip.value
  type              = "HTTP"
  port              = 80
  resource_path     = "/health/live"
  failure_threshold = 3
  request_interval  = 30

  tags = { Name = "${var.project}-primary-hc" }
}

resource "aws_route53_health_check" "standby" {
  ip_address        = data.aws_ssm_parameter.standby_public_ip.value
  type              = "HTTP"
  port              = 80
  resource_path     = "/health/live"
  failure_threshold = 3
  request_interval  = 30

  tags = { Name = "${var.project}-standby-hc" }
}

resource "aws_route53_zone" "main" {
  count = var.domain_name != "" ? 1 : 0
  name  = var.domain_name
}

resource "aws_route53_record" "failover_primary" {
  count          = var.domain_name != "" ? 1 : 0
  zone_id        = aws_route53_zone.main[0].zone_id
  name           = var.domain_name
  type           = "A"
  ttl            = 60
  records        = [data.aws_ssm_parameter.primary_public_ip.value]
  set_identifier = "primary"

  failover_routing_policy {
    type = "PRIMARY"
  }

  health_check_id = aws_route53_health_check.primary.id
}

resource "aws_route53_record" "failover_standby" {
  count          = var.domain_name != "" ? 1 : 0
  zone_id        = aws_route53_zone.main[0].zone_id
  name           = var.domain_name
  type           = "A"
  ttl            = 60
  records        = [data.aws_ssm_parameter.standby_public_ip.value]
  set_identifier = "standby"

  failover_routing_policy {
    type = "SECONDARY"
  }

  health_check_id = aws_route53_health_check.standby.id
}
