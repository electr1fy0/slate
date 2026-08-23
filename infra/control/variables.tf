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

variable "domain_name" {
  type        = string
  default     = ""
  description = "Public domain for failover DNS. Empty = skip zone/records (health checks still created)."
}

variable "alert_email" {
  type        = string
  description = "Email subscribed to Continuum alerts"
}

variable "reconciler_lookback_minutes" {
  type    = number
  default = 60
}
