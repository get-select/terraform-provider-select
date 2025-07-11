# Terraform Import Guide for Select Resources

This guide explains how to import existing Select resources into your Terraform configuration using the `terraform import` command.

## Overview

The Select Terraform provider supports importing existing resources that were created outside of Terraform. This allows you to bring existing infrastructure under Terraform management without recreating it.

## Supported Resources

- `select_usage_group_set` - Usage Group Sets
- `select_usage_group` - Usage Groups

## Prerequisites

1. Ensure you have the Select Terraform provider installed and configured
2. Have your Terraform configuration files ready with the resource definitions
3. Access to the Select UI to retrieve resource IDs

## Getting Resource IDs from Select UI

### Usage Group Set ID

1. Navigate to the Select UI
2. Go to the **Usage Groups** section
3. Select the Usage Group Set you want to import
4. The Usage Group Set ID is displayed in the URL query parameters
5. Look for the `usageGroupSetId` parameter in the URL:
   ```
   /app/<snowflake account uuid>/usage-groups/definitions?usageGroupSetId=<selected usage group set uuid>
   ```
6. Copy the UUID from the `usageGroupSetId` parameter (format: `xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx`)

**Example URL:**
```
/app/scwxhob-ad38017/usage-groups/definitions?usageGroupSetId=35b3af95-466f-4669-a3d0-916acb547710
```
In this example, the Usage Group Set ID is: `35b3af95-466f-4669-a3d0-916acb547710`

### Usage Group ID

1. Navigate to the Select UI
2. Go to the **Usage Groups** section
3. Click on the specific Usage Group Set containing the Usage Group you want to import
4. Find the Usage Group you want to import
5. **Switch from 'Interactive' mode to 'JSON' mode** to see the raw data
6. In the JSON output, locate the `usage_group_id` field for the specific usage group
7. Copy the UUID (format: `xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx`)
8. **Important**: Also note the parent Usage Group Set ID from the URL, as you'll need both IDs for the import

**Example JSON output:**
```json
[
  {
    "name": "example-usage-group",
    "budget": 1000,
    "filter_expression": {
      "operator": "or",
      "filters": [
        {
          "field": "warehouse_name",
          "values": [
            "SELECT_BACKEND",
            "SELECT_BACKEND_LARGE"
          ],
          "operator": "in"
        }
      ]
    },
    "usage_group_id": "38fccd46-6b3e-4a02-ab08-5fff826f4147"
  }
]
```

In this example, the Usage Group ID is: `38fccd46-6b3e-4a02-ab08-5fff826f4147`

**💡 Pro Tip**: The JSON mode is also useful for copying filter expressions to your Terraform configuration!

## Converting Filter Expressions from JSON to Terraform

When you find a Usage Group in JSON mode, you can easily convert the `filter_expression` to Terraform configuration.

**JSON format (from Select UI):**
```json
{
  "filter_expression": {
    "operator": "or",
    "filters": [
      {
        "field": "warehouse_name",
        "values": [
          "SELECT_BACKEND",
          "SELECT_BACKEND_LARGE"
        ],
        "operator": "in"
      }
    ]
  }
}
```

**Terraform format:**
```hcl
resource "select_usage_group" "test_group" {
  name               = "example-usage-group"
  order              = 1
  budget             = 1000.0
  usage_group_set_id = select_usage_group_set.test_set.id
  filter_expression_json = jsonencode({
    "operator" : "or",
    "filters" : [
      {
        "field" : "warehouse_name",
        "values" : ["SELECT_BACKEND", "SELECT_BACKEND_LARGE"],
        "operator" : "in",
      }
    ],
  })
}
```

**Key differences:**
- Terraform uses `jsonencode()` to convert the object to JSON
- Use colons (`:`) instead of equals (`=`) for key-value pairs inside the object
- Trailing commas are optional but recommended for easier editing

## Import Commands

### Importing a Usage Group Set

**Command Format:**
```bash
terraform import select_usage_group_set.<resource_name> <usage_group_set_id>
```

**Example:**
```bash
terraform import select_usage_group_set.production_workloads 35b3af95-466f-4669-a3d0-916acb547710
```

### Importing a Usage Group

**Command Format:**
```bash
terraform import select_usage_group.<resource_name> <usage_group_set_id>/<usage_group_id>
```

**Example:**
```bash
terraform import select_usage_group.analytics_team 35b3af95-466f-4669-a3d0-916acb547710/38fccd46-6b3e-4a02-ab08-5fff826f4147
```

**Note**: Usage Groups require a compound ID format with both the parent Usage Group Set ID and the Usage Group ID separated by a forward slash (`/`).

## Step-by-Step Import Process

### 1. Create Terraform Configuration

First, create your Terraform configuration file with the resource definitions:

```hcl
# Example: main.tf
terraform {
  required_providers {
    select = {
      source = "hashicorp.com/edu/select"
    }
  }
}

provider "select" {
  api_key         = "your-api-key"
  organization_id = "your-org-id"
}

# Usage Group Set resource definition
resource "select_usage_group_set" "production_workloads" {
  name                   = "Production Workloads"
  order                  = 1
  snowflake_account_uuid = "your-snowflake-account-uuid"
}

# Usage Group resource definition
resource "select_usage_group" "analytics_team" {
  name               = "Analytics Team"
  order              = 1
  budget             = 5000.0
  usage_group_set_id = select_usage_group_set.production_workloads.id
  filter_expression_json = jsonencode({
    "operator" : "and",
    "filters" : [
      {
        "field" : "role_name",
        "operator" : "in",
        "values" : ["ANALYST", "DATA_SCIENTIST"]
      }
    ]
  })
}
```

### 2. Run Import Commands

Import the Usage Group Set first:
```bash
terraform import select_usage_group_set.production_workloads 35b3af95-466f-4669-a3d0-916acb547710
```

Then import the Usage Group:
```bash
terraform import select_usage_group.analytics_team 35b3af95-466f-4669-a3d0-916acb547710/38fccd46-6b3e-4a02-ab08-5fff826f4147
```

### 3. Verify Import

Check the imported resources:
```bash
terraform show
```

Run a plan to see any differences:
```bash
terraform plan
```

## Common Issues and Troubleshooting

### Issue: "Invalid Import ID Format" for Usage Groups

**Error Message:**
```
Error: Invalid Import ID Format
Expected import ID in format 'usage_group_set_id/usage_group_id', got: <your_id>
```

**Solution:** Ensure you're using the correct format with both IDs separated by a forward slash:
```bash
terraform import select_usage_group.my_group <usage_group_set_id>/<usage_group_id>
```

### Issue: "Resource Not Found" Warning

**Warning Message:**
```
Warning: Resource Not Found
Resource not found at /api/org_xxx/usage-group-sets/xxx/usage-groups/xxx
```

**Possible Causes:**
1. The resource ID is incorrect
2. The resource doesn't exist in the Select system
3. You don't have permission to access the resource
4. The resource belongs to a different organization

**Solution:** 
1. Double-check the IDs from the Select UI
2. Verify you have access to the resource
3. Ensure you're using the correct organization ID in your provider configuration

### Issue: "Missing Usage Group Set ID"

**Error Message:**
```
Error: Missing Usage Group Set ID
usage_group_set_id is required but was not found in the state.
```

**Solution:** This error occurs when importing a Usage Group without the parent Usage Group Set ID. Use the compound ID format:
```bash
terraform import select_usage_group.my_group <usage_group_set_id>/<usage_group_id>
```

## Best Practices

1. **Import Dependencies First**: Always import Usage Group Sets before importing their child Usage Groups
2. **Verify Configuration**: After importing, run `terraform plan` to ensure your configuration matches the imported state
3. **Update Configuration**: Modify your Terraform configuration to match the actual resource properties shown in the state
4. **Test Changes**: After import, test that Terraform can manage the resources by making small, non-destructive changes

## Example Workflow

Here's a complete example of importing existing resources:

```bash
# 1. Initialize Terraform
terraform init

# 2. Import Usage Group Set
terraform import select_usage_group_set.production_workloads 35b3af95-466f-4669-a3d0-916acb547710

# 3. Import Usage Group
terraform import select_usage_group.analytics_team 35b3af95-466f-4669-a3d0-916acb547710/38fccd46-6b3e-4a02-ab08-5fff826f4147

# 4. Check imported state
terraform show

# 5. Plan to see differences
terraform plan

# 6. Update configuration to match imported state
# (Edit your .tf files based on the plan output)

# 7. Verify configuration matches
terraform plan
```

## Getting Help

If you encounter issues not covered in this guide:

1. Check the Terraform and provider logs for detailed error messages
2. Verify your resource IDs are correct in the Select UI
3. Ensure your provider configuration (API key, organization ID) is correct
4. Contact your Select administrator for assistance with resource access 
