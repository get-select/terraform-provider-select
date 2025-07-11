# Terraform Provider Tests

Integration tests for the Select Terraform provider using Terraform's built-in testing framework.

## Setup

### Prerequisites

1. **Set Environment Variables**:
   ```bash
   export TF_VAR_select_api_key="your-api-key"
   export TF_VAR_select_organization_id="your-org-id"
   ```

2. **Install Provider**:
   ```bash
   make install
   ```

## Running Tests

### Run All Tests
```bash
make test
```

### Run Individual Test Cases
```bash
# From the tests directory
terraform test provider.tftest.hcl

# Run specific test case
terraform test provider.tftest.hcl -filter=create_usage_group_set
```

### Clean Up Test Resources
```bash
make test-clean
```

## Test Files

- **`main.tf`** - Test configuration with resources and variables
- **`provider.tftest.hcl`** - Test cases that validate provider functionality

## Troubleshooting

### Authentication Errors
```
Error: Unauthorized
```
**Solution**: Verify your environment variables are set correctly.

### Provider Not Found
```
Error: Could not find required provider
```
**Solution**: Run `make install` to build and install the provider.

### Resource Conflicts
```
Error: Resource already exists
```
**Solution**: Run `make test-clean` to remove leftover test resources. 
