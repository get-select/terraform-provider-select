# SELECT reads the project's billing and pricing export from a dataset in the
# project itself, impersonating a service account with read access to it.
resource "select_bigquery_connection" "production" {
  name = "Acme Production"

  gcp_project_id      = "acme-prod"
  bigquery_dataset_id = "billing_export"
  billing_account_id  = "012345-6789AB-CDEF01"
  service_account     = "select@acme-prod.iam.gserviceaccount.com"
}
