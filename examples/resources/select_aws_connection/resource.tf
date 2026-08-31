# SELECT reads an AWS payer account's spend out of the Cost and Usage Report AWS
# delivers to S3. The credentials are an IAM user's access key pair with
# permission to list and read the report objects under the prefix — SELECT does
# not assume a role, so there is no role ARN or external id here.
resource "select_aws_connection" "production" {
  name = "Acme Production"

  payer_account_id = "123456789012"
  s3_bucket        = "acme-cost-and-usage-reports"
  s3_prefix        = "cur/production"
  region           = "us-east-1"

  credentials = {
    access_key_id     = var.aws_access_key_id
    secret_access_key = var.aws_secret_access_key
  }
}

# A report delivered to the root of its bucket has no prefix. Omit s3_prefix
# rather than setting it to "", which SELECT stores as an empty prefix rather
# than as no prefix at all.
resource "select_aws_connection" "sandbox" {
  name = "Acme Sandbox"

  payer_account_id = "210987654321"
  s3_bucket        = "acme-sandbox-cur"
  region           = "eu-west-1"

  credentials = {
    access_key_id     = var.aws_sandbox_access_key_id
    secret_access_key = var.aws_sandbox_secret_access_key
  }

  # Hold off syncing until the connection has been reviewed.
  sync_enabled = false
}
