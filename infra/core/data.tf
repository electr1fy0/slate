resource "aws_s3_bucket" "bundle_primary" {
  bucket        = "${var.project}-${data.aws_caller_identity.current.account_id}-bundle-pri"
  force_destroy = true
}

resource "aws_s3_bucket_public_access_block" "bundle_primary" {
  bucket                  = aws_s3_bucket.bundle_primary.id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket" "bundle_standby" {
  provider      = aws.standby
  bucket        = "${var.project}-${data.aws_caller_identity.current.account_id}-bundle-stb"
  force_destroy = true
}

resource "aws_s3_bucket_public_access_block" "bundle_standby" {
  provider                = aws.standby
  bucket                  = aws_s3_bucket.bundle_standby.id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket" "assets_primary" {
  bucket        = "${var.project}-${data.aws_caller_identity.current.account_id}-assets-pri"
  force_destroy = true
}

resource "aws_s3_bucket_versioning" "assets_primary" {
  bucket = aws_s3_bucket.assets_primary.id
  versioning_configuration {
    status = "Enabled"
  }
}

resource "aws_s3_bucket" "assets_standby" {
  provider      = aws.standby
  bucket        = "${var.project}-${data.aws_caller_identity.current.account_id}-assets-stb"
  force_destroy = true
}

resource "aws_s3_bucket_versioning" "assets_standby" {
  provider = aws.standby
  bucket   = aws_s3_bucket.assets_standby.id
  versioning_configuration {
    status = "Enabled"
  }
}

data "aws_iam_policy_document" "replication_assume" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["s3.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "s3_replication" {
  name               = "${var.project}-s3-replication-role"
  assume_role_policy = data.aws_iam_policy_document.replication_assume.json
}

resource "aws_iam_role_policy" "s3_replication" {
  name = "${var.project}-s3-replication-policy"
  role = aws_iam_role.s3_replication.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect   = "Allow"
        Action   = ["s3:GetReplicationConfiguration", "s3:ListBucket"]
        Resource = [aws_s3_bucket.assets_primary.arn]
      },
      {
        Effect   = "Allow"
        Action   = ["s3:GetObjectVersionForReplication", "s3:GetObjectVersionAcl", "s3:GetObjectVersionTagging"]
        Resource = ["${aws_s3_bucket.assets_primary.arn}/*"]
      },
      {
        Effect   = "Allow"
        Action   = ["s3:ReplicateObject", "s3:ReplicateDelete", "s3:ReplicateTags"]
        Resource = ["${aws_s3_bucket.assets_standby.arn}/*"]
      }
    ]
  })
}

resource "aws_s3_bucket_replication_configuration" "assets_to_standby" {
  depends_on = [aws_s3_bucket_versioning.assets_primary, aws_s3_bucket_versioning.assets_standby]
  bucket     = aws_s3_bucket.assets_primary.id
  role       = aws_iam_role.s3_replication.arn

  rule {
    id     = "crr-to-standby"
    status = "Enabled"

    delete_marker_replication {
      status = "Enabled"
    }

    destination {
      bucket        = aws_s3_bucket.assets_standby.arn
      storage_class = "STANDARD"
    }
  }
}

resource "aws_cloudtrail" "main" {
  count                      = var.enable_cloudtrail ? 1 : 0
  name                       = "${var.project}-trail"
  s3_bucket_name             = aws_s3_bucket.assets_primary.id
  s3_key_prefix              = "cloudtrail"
  enable_log_file_validation = true
  depends_on                 = [aws_s3_bucket.assets_primary]
}

data "aws_iam_policy_document" "cloudtrail_bucket" {
  count = var.enable_cloudtrail ? 1 : 0

  statement {
    sid       = "AWSCloudTrailAclCheck"
    effect    = "Allow"
    actions   = ["s3:GetBucketAcl"]
    resources = [aws_s3_bucket.assets_primary.arn]
    principals {
      type        = "Service"
      identifiers = ["cloudtrail.amazonaws.com"]
    }
  }

  statement {
    sid       = "AWSCloudTrailWrite"
    effect    = "Allow"
    actions   = ["s3:PutObject"]
    resources = ["${aws_s3_bucket.assets_primary.arn}/cloudtrail/AWSLogs/${data.aws_caller_identity.current.account_id}/*"]
    principals {
      type        = "Service"
      identifiers = ["cloudtrail.amazonaws.com"]
    }
    condition {
      test     = "StringEquals"
      variable = "s3:x-amz-acl"
      values   = ["bucket-owner-full-control"]
    }
  }
}

resource "aws_s3_bucket_policy" "cloudtrail" {
  count  = var.enable_cloudtrail ? 1 : 0
  bucket = aws_s3_bucket.assets_primary.id
  policy = data.aws_iam_policy_document.cloudtrail_bucket[0].json
}
