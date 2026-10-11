resource "aws_vpc" "demo" {
  cidr_block           = "10.55.0.0/16"
  enable_dns_support   = true
  enable_dns_hostnames = true
  tags = {
    Name = var.project
  }
}
resource "aws_internet_gateway" "demo" {
  vpc_id = aws_vpc.demo.id
}
resource "aws_subnet" "app" {
  for_each                = local.nodes
  vpc_id                  = aws_vpc.demo.id
  cidr_block              = cidrsubnet(aws_vpc.demo.cidr_block, 8, each.value + 1)
  availability_zone       = data.aws_availability_zones.available.names[each.value]
  map_public_ip_on_launch = true
  tags = {
    Name = "${var.project}-app-${each.key}"
  }
}
resource "aws_subnet" "lambda" {
  for_each          = local.nodes
  vpc_id            = aws_vpc.demo.id
  cidr_block        = cidrsubnet(aws_vpc.demo.cidr_block, 8, each.value + 11)
  availability_zone = data.aws_availability_zones.available.names[each.value]
  tags = {
    Name = "${var.project}-lambda-${each.key}"
  }
}
resource "aws_route_table" "app" {
  vpc_id = aws_vpc.demo.id
  route {
    cidr_block = "0.0.0.0/0"
    gateway_id = aws_internet_gateway.demo.id
  }
}
resource "aws_route_table" "lambda" {
  vpc_id = aws_vpc.demo.id
}
resource "aws_route_table_association" "app" {
  for_each       = local.nodes
  subnet_id      = aws_subnet.app[each.key].id
  route_table_id = aws_route_table.app.id
}
resource "aws_route_table_association" "lambda" {
  for_each       = local.nodes
  subnet_id      = aws_subnet.lambda[each.key].id
  route_table_id = aws_route_table.lambda.id
}
resource "aws_vpc_endpoint" "gateway" {
  for_each          = toset(["s3", "dynamodb"])
  vpc_id            = aws_vpc.demo.id
  service_name      = "com.amazonaws.${var.region}.${each.key}"
  vpc_endpoint_type = "Gateway"
  route_table_ids   = [aws_route_table.app.id, aws_route_table.lambda.id]
}
resource "aws_security_group" "lambda" {
  name_prefix = "${var.project}-lambda-"
  vpc_id      = aws_vpc.demo.id
  description = "Private API routing and verification"
  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }
}
resource "aws_security_group" "app" {
  name_prefix = "${var.project}-app-"
  vpc_id      = aws_vpc.demo.id
  description = "Only private router/verifier can reach application; no SSH"
  ingress {
    from_port       = 8000
    to_port         = 8000
    protocol        = "tcp"
    security_groups = [aws_security_group.lambda.id]
  }
  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }
}
