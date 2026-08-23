variable "project" {
  type = string
}

variable "region_label" {
  type = string
}

variable "instance_type" {
  type    = string
  default = "t3.micro"
}

variable "subnet_id" {
  type = string
}

variable "security_group_id" {
  type = string
}

variable "app_role" {
  type        = string
  description = "active or standby"
}

variable "table_name" {
  type = string
}

variable "bundle_bucket" {
  type = string
}

variable "ami_ssm_path" {
  type    = string
  default = "/aws/service/ami-amazon-linux-latest/al2023-ami-kernel-default-x86_64"
}
