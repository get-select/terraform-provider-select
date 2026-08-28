# A customer-owned connection: SELECT reads the project's billing and pricing
# export from a dataset in the project itself, impersonating a service account
# with read access to it.
resource "select_bigquery_connection" "production" {
  name = "Acme Production"

  gcp_project_id      = "acme-prod"
  bigquery_dataset_id = "billing_export"
  billing_account_id  = "012345-6789AB-CDEF01"
  service_account     = "select@acme-prod.iam.gserviceaccount.com"
}

# A DoiT-managed connection reads DoiT's own billing data instead, so
# bigquery_dataset_id and billing_account_id are omitted rather than set. Adding
# one with is_doit = true needs a signed-in user, not an API key — this
# provider only ever holds an API key, so a connection like this has to be
# created in the SELECT UI and brought under Terraform with `terraform import`
# rather than created here.
resource "select_bigquery_connection" "doit_managed" {
  name = "Acme DoiT Reseller"

  gcp_project_id  = "acme-doit"
  service_account = "select@acme-doit.iam.gserviceaccount.com"
  is_doit         = true
}
