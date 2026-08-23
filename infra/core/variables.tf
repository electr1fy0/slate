variable "project" {
  type    = string
  default = "continuum"
}

variable "primary_region" {
  type    = string
  default = "ap-south-1"
}

variable "standby_region" {
  type    = string
  default = "ap-southeast-1"
}

variable "primary_az" {
  type    = string
  default = "ap-south-1a"
}

variable "instance_type" {
  type    = string
  default = "t3.micro"
}

variable "admin_cidrs" {
  type    = list(string)
  default = ["0.0.0.0/0"]
}

variable "admin_token" {
  type        = string
  sensitive   = true
  description = "Token for app /admin endpoints"
}

variable "enable_cloudtrail" {
  type    = bool
  default = true
}
