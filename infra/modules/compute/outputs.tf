output "instance_id" {
  value = aws_instance.app.id
}

output "public_ip" {
  value = aws_eip.app.public_ip
}

output "instance_profile_arn" {
  value = aws_iam_instance_profile.app.arn
}
