variable "project" {
  type = string
}

variable "region_label" {
  type = string
}

variable "vpc_cidr" {
  type    = string
  default = "10.20.0.0/16"
}

variable "availability_zone" {
  type = string
}

variable "admin_cidrs" {
  type        = list(string)
  default     = ["0.0.0.0/0"]
  description = "CIDRs allowed SSH; restrict in real deployments"
}
