data "aws_iam_policy_document" "lambda_trust" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["lambda.amazonaws.com"]
    }
  }
}
data "aws_iam_policy_document" "ec2_trust" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["ec2.amazonaws.com"]
    }
  }
}
resource "aws_iam_role" "lambda" {
  for_each           = local.function_names
  name               = each.value
  assume_role_policy = data.aws_iam_policy_document.lambda_trust.json
}
resource "aws_iam_role_policy_attachment" "logs" {
  for_each   = local.function_names
  role       = aws_iam_role.lambda[each.key].name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole"
}
resource "aws_iam_role_policy_attachment" "vpc" {
  for_each   = toset(["router", "demo-verifier"])
  role       = aws_iam_role.lambda[each.key].name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AWSLambdaVPCAccessExecutionRole"
}
resource "aws_iam_role_policy" "lambda" {
  for_each = local.function_names
  role     = aws_iam_role.lambda[each.key].id
  name     = "generated-runtime"
  policy = templatefile("${path.module}/policies/${each.key}.json.tftpl", {
    table_arn         = aws_dynamodb_table.demo.arn,
    verifier_arn      = local.function_arns["demo-verifier"],
    control_arn       = local.function_arns["demo-control"],
    monitor_arn       = local.function_arns["demo-monitor"],
    reconciler_arn    = "${local.prefix}:lambda:${var.region}:${data.aws_caller_identity.current.account_id}:function:${var.project}-demo-reconciler",
    state_machine_arn = local.state_machine_arn,
    instance_arns     = jsonencode([for node in aws_instance.app : node.arn])
    }
  )
}
resource "aws_iam_role" "app" {
  name               = "${var.project}-app"
  assume_role_policy = data.aws_iam_policy_document.ec2_trust.json
}
resource "aws_iam_role_policy_attachment" "ssm" {
  role       = aws_iam_role.app.name
  policy_arn = "arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"
}
resource "aws_iam_role_policy" "app" {
  role = aws_iam_role.app.id
  name = "generated-runtime"
  policy = templatefile("${path.module}/policies/app.json.tftpl", {
    table_arn = aws_dynamodb_table.demo.arn
    }
  )
}
data "aws_kms_key" "ssm" {
  key_id     = "alias/aws/ssm"
  depends_on = [aws_ssm_parameter.runtime]
}
resource "aws_iam_role_policy" "bootstrap" {
  role = aws_iam_role.app.id
  name = "bootstrap-artifacts-and-runtime"
  policy = jsonencode({
    Version = "2012-10-17", Statement = [
      {
        Effect = "Allow", Action = ["s3:GetObject"], Resource = ["${aws_s3_bucket.bundle.arn}/app/app.tar.gz"]
      },
      {
        Effect = "Allow", Action = ["ssm:GetParameter"], Resource = [aws_ssm_parameter.runtime.arn]
      },
      {
        Effect = "Allow", Action = ["kms:Decrypt"], Resource = [data.aws_kms_key.ssm.arn], Condition = {
          StringEquals = {
            "kms:ViaService" = "ssm.${var.region}.amazonaws.com"
          }
        }
      }
    ]
    }
  )
}
resource "aws_iam_instance_profile" "app" {
  name = "${var.project}-app"
  role = aws_iam_role.app.name
}
resource "aws_iam_role" "state_machine" {
  name = "${var.project}-failover"
  assume_role_policy = jsonencode({
    Version = "2012-10-17", Statement = [{
      Effect = "Allow", Action = "sts:AssumeRole", Principal = {
        Service = "states.amazonaws.com"
      },
      Condition = {
        StringEquals = {
          "aws:SourceAccount" = data.aws_caller_identity.current.account_id
        },
        ArnEquals = {
          "aws:SourceArn" = local.state_machine_arn
        }
      }
      }
    ]
    }
  )
}
resource "aws_iam_role_policy" "state_machine" {
  role = aws_iam_role.state_machine.id
  name = "generated-workflow"
  policy = templatefile("${path.module}/policies/workflow.json.tftpl", {
    verifier_arn = local.function_arns["demo-verifier"], control_arn = local.function_arns["demo-control"], reconciler_arn = aws_lambda_function.reconciler.arn
    }
  )
}
