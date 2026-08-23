data "aws_caller_identity" "current" {}

module "network_primary" {
  source            = "../modules/network"
  project           = var.project
  region_label      = "primary"
  availability_zone = var.primary_az
  admin_cidrs       = var.admin_cidrs
}

module "compute_primary" {
  source            = "../modules/compute"
  project           = var.project
  region_label      = "primary"
  instance_type     = var.instance_type
  subnet_id         = module.network_primary.subnet_id
  security_group_id = module.network_primary.security_group_id
  app_role          = "active"
  table_name        = aws_dynamodb_table.continuum.name
  bundle_bucket     = aws_s3_bucket.bundle_primary.id
}

resource "aws_dynamodb_table" "continuum" {
  name         = "${var.project}-state"
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "pk"
  range_key    = "sk"

  attribute {
    name = "pk"
    type = "S"
  }
  attribute {
    name = "sk"
    type = "S"
  }

  ttl {
    attribute_name = "ttl"
    enabled        = true
  }

  stream_enabled   = true
  stream_view_type = "NEW_AND_OLD_IMAGES"

  replica {
    region_name = var.standby_region
  }

  tags = { Name = "${var.project}-global-table" }
}
