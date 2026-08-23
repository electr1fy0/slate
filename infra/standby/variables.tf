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

variable "standby_az" {
  type    = string
  default = "ap-southeast-1b"
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
  description = "Must match core stack value; used by promote flows"
}
