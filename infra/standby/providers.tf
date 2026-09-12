terraform {
  required_version = ">= 1.6"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.60"
    }
  }
}

provider "aws" {
  region = var.standby_region
  default_tags {
    tags = {
      Project       = var.project
      ManagedBy     = "terraform"
      ContinuumRole = "standby"
    }
  }
}

provider "aws" {
  alias  = "primary"
  region = var.primary_region
}
