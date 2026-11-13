# SELECT reads an AWS account's spend out of the Cost and Usage Report AWS
# delivers to S3. Here it assumes a role in the account that can list and read
# the report objects under the prefix. The role's trust policy names SELECT's
# AWS account, with the external ID shown in SELECT as the sts:ExternalId
# condition.
resource "select_aws_connection" "production" {
  name = "Acme Production"

  aws_account_id = "123456789012"
  s3_bucket      = "acme-cost-and-usage-reports"
  s3_prefix      = "cur/production"
  region         = "us-east-1"

  credentials = {
    authentication_method = "role"
    role_arn              = "arn:aws:iam::123456789012:role/select-cur-read"
  }
}

# A report delivered to the root of its bucket has no prefix. Omit s3_prefix
# rather than setting it to "", which SELECT stores as an empty prefix rather
# than as no prefix at all.
resource "select_aws_connection" "sandbox" {
  name = "Acme Sandbox"

  aws_account_id = "210987654321"
  s3_bucket      = "acme-sandbox-cur"
  region         = "eu-west-1"

  # An IAM user's access key pair works too, with the same read access.
  credentials = {
    authentication_method = "access_key"
    access_key_id         = var.aws_sandbox_access_key_id
    secret_access_key     = var.aws_sandbox_secret_access_key
  }

  # Hold off syncing until the connection has been reviewed.
  sync_enabled = false
}
