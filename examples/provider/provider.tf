terraform {
  required_providers {
    select = {
      source  = "get-select/select"
      version = "~> 0.1"
    }
  }
}

provider "select" {
  api_key         = var.select_api_key
  organization_id = var.select_organization_id
} 