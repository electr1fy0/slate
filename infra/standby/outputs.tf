output "standby_instance_id" {
  value = module.compute_standby.instance_id
}

output "standby_public_ip" {
  value = module.compute_standby.public_ip
}

output "app_url" {
  value = "http://${module.compute_standby.public_ip}"
}
