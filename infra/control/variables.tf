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
  type        = number
  default     = 10080
  description = "Journal scan window for the reconciler. Aligned with the journal TTL (7 days) so nothing expires before replay."
}

variable "reconciler_schedule_minutes" {
  type        = number
  default     = 15
  description = "Cadence of the scheduled outbox sweep between failovers."
}
