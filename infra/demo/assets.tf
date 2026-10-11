locals {
  frontend_build = "${path.module}/../../web/dist/client"
  content_types = {
    html  = "text/html; charset=utf-8"
    js    = "application/javascript"
    css   = "text/css"
    json  = "application/json"
    svg   = "image/svg+xml"
    png   = "image/png"
    ico   = "image/x-icon"
    webp  = "image/webp"
    woff2 = "font/woff2"
  }
}
resource "aws_s3_object" "frontend" {
  for_each               = fileset(local.frontend_build, "**")
  bucket                 = aws_s3_bucket.frontend.id
  key                    = each.value
  source                 = "${local.frontend_build}/${each.value}"
  source_hash            = filemd5("${local.frontend_build}/${each.value}")
  content_type           = lookup(local.content_types, reverse(split(".", each.value))[0], "application/octet-stream")
  cache_control          = endswith(each.value, ".html") ? "no-cache" : "public, max-age=31536000, immutable"
  server_side_encryption = "AES256"
}
