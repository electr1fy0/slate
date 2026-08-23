output "subnet_id" {
  value = aws_subnet.app.id
}

output "security_group_id" {
  value = aws_security_group.app.id
}

output "vpc_id" {
  value = aws_vpc.main.id
}
