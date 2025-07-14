# Terraform Provider for Select

The Select Terraform Provider enables you to manage resources in the Select data platform using Terraform. SELECT helps organizations optimize their Snowflake usage, for more information visit our website https://select.dev/ .

## Requirements

- [Terraform](https://www.terraform.io/downloads.html) >= 0.13
- [Go](https://golang.org/doc/install) >= 1.20 (for building the provider)

## Installation

### Terraform Registry

<!-- TODO for humans: Update registry path once published to official Terraform Registry -->
```hcl
terraform {
  required_providers {
    select = {
      source  = "hashicorp.com/edu/select"
      version = "~> 0.1"
    }
  }
}
```

### Local Development

For local development, you can build and install the provider locally:

```bash
git clone https://github.com/TODO_REPO_URL/terraform-provider-select
cd terraform-provider-select
make install
```

## Usage

### Provider Configuration

```hcl
provider "select" {
  api_key         = var.select_api_key
  organization_id = var.select_organization_id
  # select_api_url = "https://api.select.dev"  # Optional, defaults to https://api.select.dev
}
```

### Configuration Options

- `api_key` (Required, String, Sensitive) - The API key for authenticating with the Select API. This key must have write access to the resources you wish to create. API Keys can be created in the API Keys tab on the settings page of the Select app.
- `organization_id` (Required, String) - The organization ID for the Select account. Available from the profile overview tab on the settings page in the Select app.
- `select_api_url` (Optional, String) - The base URL for the Select API. Defaults to `https://api.select.dev`.

### Basic Example

```hcl
terraform {
  required_providers {
    select = {
      source  = "hashicorp.com/edu/select"
      version = "~> 0.1"
    }
  }
}

provider "select" {
  api_key         = var.select_api_key
  organization_id = var.select_organization_id
}

# Create a usage group set
resource "select_usage_group_set" "production" {
  name                   = "Production Workloads"
  order                  = 1
  snowflake_account_uuid = var.snowflake_account_uuid
}

# Create a usage group
resource "select_usage_group" "analytics" {
  name               = "Analytics Team"
  order              = 1
  budget             = 5000.0
  usage_group_set_id = select_usage_group_set.production.id
  
  filter_expression_json = jsonencode({
    operator = "and"
    filters = [
      {
        field    = "role_name"
        operator = "in"
        values   = ["ANALYST", "DATA_SCIENTIST"]
      }
    ]
  })
}
```

## Available Resources

### Resources

- [`select_usage_group_set`](docs/resources/usage_group_set.md) - Manages usage group sets, which are logical groupings of usage groups
- [`select_usage_group`](docs/resources/usage_group.md) - Manages individual usage groups within a usage group set

### Data Sources

<!-- TODO for humans: Add data sources documentation when available -->
Currently, this provider does not expose any data sources.

## Documentation

- [Provider Documentation](docs/index.md)
- [Import Guide](IMPORT.md) - Comprehensive guide for importing existing resources
- [Resource Documentation](docs/resources/)

## Authentication

The Select provider requires an API key and organization ID for authentication:

1. **API Key**: Generate an API key in the Select app:
   - Navigate to Settings → API Keys
   - Create a new API key with write permissions
   
2. **Organization ID**: Find your organization ID in the Select app:
   - Navigate to Settings → Profile Overview
   - Copy the organization ID

### Environment Variables

You can set credentials using environment variables:

```bash
export TF_VAR_select_api_key="your-api-key-here"
export TF_VAR_select_organization_id="your-org-id-here"
```

## Examples

More comprehensive examples can be found in the [examples](examples/) directory.

## Importing Existing Resources

The Select provider supports importing existing resources created outside of Terraform. See the [Import Guide](IMPORT.md) for detailed instructions.

Quick reference:
- **Usage Group Set**: `terraform import select_usage_group_set.example <usage_group_set_id>`
- **Usage Group**: `terraform import select_usage_group.example <usage_group_set_id>/<usage_group_id>`

## Development

### Building the Provider

```bash
git clone https://github.com/TODO_REPO_URL/terraform-provider-select
cd terraform-provider-select
make build
```

### Local Installation

```bash
make install
```

### Running Tests

```bash
# Set required environment variables
export TF_VAR_select_api_key="your-test-api-key"
export TF_VAR_select_organization_id="your-test-org-id"

# Run tests
make test
```

### Development Setup

For local development with Terraform, you can use development overrides:

```bash
make setup-dev-overrides
```

This will configure Terraform to use your locally built provider instead of downloading from the registry.

## Contributing

<!-- TODO for humans: Add contributing guidelines URL once repository is public -->
We welcome contributions! Please see our [Contributing Guide](CONTRIBUTING.md) for details.

### Reporting Issues

If you encounter any issues, please [open an issue](https://github.com/TODO_REPO_URL/terraform-provider-select/issues) with:
- Terraform version
- Provider version
- A minimal reproduction case
- Full error messages

## License

This project is licensed under the Mozilla Public License 2.0 - see https://mozilla.org/MPL/2.0/ for details.

## Support

- [GitHub Issues](https://github.com/TODO_REPO_URL/terraform-provider-select/issues)
- [Select Documentation](https://select.dev/docs)

## Changelog

See [CHANGELOG.md](CHANGELOG.md) for information about changes in each release.

---

**Note**: This provider is for managing Select platform resources. For general Snowflake resource management, use the [Snowflake Terraform Provider](https://registry.terraform.io/providers/Snowflake-Labs/snowflake/latest).
