data "aws_ssm_parameter" "ami" {
  name = "/aws/service/ami-amazon-linux-latest/al2023-ami-kernel-default-x86_64"
}
resource "aws_instance" "app" {
  for_each                    = local.nodes
  ami                         = data.aws_ssm_parameter.ami.value
  instance_type               = var.instance_type
  subnet_id                   = aws_subnet.app[each.key].id
  vpc_security_group_ids      = [aws_security_group.app.id]
  associate_public_ip_address = true
  iam_instance_profile        = aws_iam_instance_profile.app.name
  user_data = templatefile("${path.module}/user-data.sh.tftpl", {
    region         = var.region, backend = each.key, table = aws_dynamodb_table.demo.name,
    bundle_bucket  = aws_s3_bucket.bundle.id, runtime_parameter = aws_ssm_parameter.runtime.name,
    teacher_email  = var.teacher_email
    bundle_version = fileexists("${path.module}/../../build/demo/app.tar.gz") ? filebase64sha256("${path.module}/../../build/demo/app.tar.gz") : "build-first"
    }
  )
  user_data_replace_on_change = true
  lifecycle {
    # The observed provider refresh reports this launch setting as false
    # after StopInstances releases the automatic IPv4 address. Preserve it
    # at creation without replacing a deliberately stopped demo instance.
    ignore_changes = [associate_public_ip_address]
  }
  credit_specification {
    cpu_credits = "standard"
  }
  root_block_device {
    volume_size           = 10
    volume_type           = "gp3"
    encrypted             = true
    delete_on_termination = true
  }
  metadata_options {
    http_tokens   = "required"
    http_endpoint = "enabled"
  }
  tags = {
    Name = "${var.project}-${each.key}", Backend = each.key
  }
  depends_on = [aws_s3_object.bundle, aws_iam_role_policy.app, aws_iam_role_policy.bootstrap, aws_iam_role_policy_attachment.ssm, aws_route_table_association.app, aws_vpc_endpoint.gateway]
}
resource "aws_ec2_instance_state" "demo" {
  for_each    = local.nodes
  instance_id = aws_instance.app[each.key].id
  state       = var.instances_running ? "running" : "stopped"
}
