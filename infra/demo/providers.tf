terraform {
  required_version = ">= 1.6"
  required_providers {
    aws = {
      source = "hashicorp/aws", version = "~> 6.0"
    }
    random = {
      source = "hashicorp/random", version = "~> 3.7"
    }
  }
}
provider "aws" {
  region = var.region
  default_tags {
    tags = {
      Project = var.project, ManagedBy = "terraform", Purpose = "faculty-demo"
    }
  }
}
data "aws_caller_identity" "current" {
}
data "aws_partition" "current" {
}
data "aws_availability_zones" "available" {
  state = "available"
}
locals {
  prefix          = "arn:${data.aws_partition.current.partition}"
  regional_prefix = "${local.prefix}:${var.region}:${data.aws_caller_identity.current.account_id}"
  nodes = {
    A = 0, B = 1
  }
  function_names = {
    router        = "${var.project}-router"
    demo-control  = "${var.project}-demo-control"
    demo-verifier = "${var.project}-demo-verifier"
    demo-monitor  = "${var.project}-demo-monitor"
  }
  function_arns = {
    for k, v in local.function_names : k => "${local.prefix}:lambda:${var.region}:${data.aws_caller_identity.current.account_id}:function:${v}"
  }
  state_machine_arn = "${local.prefix}:states:${var.region}:${data.aws_caller_identity.current.account_id}:stateMachine:${var.project}-failover"
}
