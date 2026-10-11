resource "aws_dynamodb_table" "demo" {
  name         = var.project
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "pk"
  range_key    = "sk"
  attribute {
    name = "pk"
    type = "S"
  }
  attribute {
    name = "sk"
    type = "S"
  }
  ttl {
    attribute_name = "ttl"
    enabled        = true
  }
  server_side_encryption {
    enabled = true
  }
}
resource "aws_dynamodb_table_item" "active" {
  table_name = aws_dynamodb_table.demo.name
  hash_key   = aws_dynamodb_table.demo.hash_key
  range_key  = aws_dynamodb_table.demo.range_key
  item = jsonencode({
    pk = {
      S = "CONTROL#ACTIVE"
    },
    sk = {
      S = "STATE"
    },
    active_backend = {
      S = "A"
    },
    epoch = {
      N = "1"
    },
    paused = {
      BOOL = false
    }
    }
  )
  lifecycle {
    ignore_changes = [item]
  }
}
resource "random_password" "admin" {
  length  = 40
  special = false
}
resource "random_password" "jwt" {
  length  = 64
  special = false
}
resource "random_password" "teacher" {
  length  = 24
  special = false
}
resource "aws_ssm_parameter" "runtime" {
  name = "/${var.project}/runtime"
  type = "SecureString"
  value = jsonencode({
    ADMIN_TOKEN = random_password.admin.result, JWT_SECRET = random_password.jwt.result, BOOTSTRAP_TEACHER_PASSWORD = random_password.teacher.result
    }
  )
}
resource "aws_s3_bucket" "bundle" {
  bucket_prefix = "${var.project}-bundle-"
  force_destroy = true
}
resource "aws_s3_bucket_public_access_block" "bundle" {
  bucket                  = aws_s3_bucket.bundle.id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}
resource "aws_s3_bucket_server_side_encryption_configuration" "bundle" {
  bucket = aws_s3_bucket.bundle.id
  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "AES256"
    }
  }
}
resource "aws_s3_object" "bundle" {
  bucket                 = aws_s3_bucket.bundle.id
  key                    = "app/app.tar.gz"
  source                 = "${path.module}/../../build/demo/app.tar.gz"
  source_hash            = fileexists("${path.module}/../../build/demo/app.tar.gz") ? filemd5("${path.module}/../../build/demo/app.tar.gz") : null
  server_side_encryption = "AES256"
}
