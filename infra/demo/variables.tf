variable "region" {
  type        = string
  default     = "ap-southeast-2"
  description = "Selected project Region. Confirm in AWS Settings before deployment."
  validation {
    condition     = var.region == "ap-southeast-2"
    error_message = "This deployment is locked to the confirmed Sydney project Region."
  }
}
variable "project" {
  type    = string
  default = "continuum-demo"
  validation {
    condition     = can(regex("^[a-z][a-z0-9-]{2,28}$", var.project))
    error_message = "Use a short lowercase project resource prefix."
  }
}
variable "monitor_enabled" {
  type        = bool
  default     = false
  description = "Enable only during active demonstration windows."
}
variable "instance_type" {
  type    = string
  default = "t3.micro"
  validation {
    condition     = contains(["t3.micro", "t3.small"], var.instance_type)
    error_message = "Use a bounded small demo instance type."
  }
}
variable "teacher_email" {
  type    = string
  default = "faculty@continuum.test"
}
variable "log_retention_days" {
  type    = number
  default = 7
}
variable "instances_running" {
  type        = bool
  default     = true
  description = "Set false between rehearsals; disks and small stored data still use credits."
}
