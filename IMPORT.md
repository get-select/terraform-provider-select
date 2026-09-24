# Terraform Import Guide for SELECT Resources

This guide explains how to import existing SELECT resources into your Terraform configuration using the `terraform import` command.

## Overview

The SELECT Terraform provider supports importing existing resources that were created outside of Terraform. This allows you to bring existing infrastructure under Terraform management without recreating it.

## Supported Resources

- `select_usage_group_set` - Usage Group Sets
- `select_usage_group` - Usage Groups
- `select_snowflake_account` - Snowflake Accounts
- `select_databricks_connection` - Databricks Connections
- `select_bigquery_connection` - BigQuery Connections
- `select_aws_connection` - AWS Connections
- `select_budget` - Budgets

## Prerequisites

1. Ensure you have the SELECT Terraform provider installed and configured
2. Have your Terraform configuration files ready with the resource definitions
3. Access to the SELECT UI to retrieve resource IDs

## Getting Resource IDs from SELECT UI

### Usage Group Set ID

1. Navigate to the SELECT UI
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

Or list them with the API:

```bash
curl -s https://api.select.dev/v2/usage-group-sets \
  -H "Authorization: Bearer $SELECT_API_KEY" \
  -H "x-tenant-id: $SELECT_ORGANIZATION_ID" | jq '.items[] | {id, name, team_id}'
```

### Usage Group ID

1. Navigate to the SELECT UI
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

### Snowflake Account ID

The ID is the Snowflake account's own identifier, so there is nothing to look up in the SELECT UI. It is the `ORGANIZATION-ACCOUNT` value you would use to connect to the account, normalized: lower case, no surrounding whitespace, and a `-` rather than a `.` between the organization and account names.

Running `select current_organization_name(), current_account_name();` in Snowflake gives you both halves. For `ACME` and `US_EAST_1`, the ID is `acme-us_east_1`.

A PrivateLink identifier keeps its dots, because the host it resolves to does: `acme-us-east-1.privatelink`.

### Databricks Connection ID

Unlike a Snowflake account, a Databricks connection's ID is assigned by SELECT when the connection is added, so there is nothing about your Databricks setup that predicts it.

Open the connection in the SELECT UI and take the last segment of the URL, or list them with the API:

```bash
curl -s https://api.select.dev/v2/databricks-connections \
  -H "Authorization: Bearer $SELECT_API_KEY" \
  -H "x-tenant-id: $SELECT_ORGANIZATION_ID" | jq '.items[] | {id, name, databricks_account_id}'
```

### BigQuery Connection ID

Like a Databricks connection and unlike a Snowflake account, a BigQuery connection's ID is assigned by SELECT when the connection is added, so there is nothing about your GCP project that predicts it.

Open the connection in the SELECT UI and take the last segment of the URL, or list them with the API:

```bash
curl -s https://api.select.dev/v2/bigquery-connections \
  -H "Authorization: Bearer $SELECT_API_KEY" \
  -H "x-tenant-id: $SELECT_ORGANIZATION_ID" | jq '.items[] | {id, name, gcp_project_id}'
```

### AWS Connection ID

Like a Databricks or BigQuery connection, an AWS connection's ID is assigned by SELECT when the connection is added, so there is nothing about your AWS account that predicts it. Note that the API calls this resource an AWS account.

Open the connection in the SELECT UI and take the last segment of the URL, or list them with the API:

```bash
curl -s https://api.select.dev/v2/aws-accounts \
  -H "Authorization: Bearer $SELECT_API_KEY" \
  -H "x-tenant-id: $SELECT_ORGANIZATION_ID" | jq '.items[] | {id, name, payer_account_id}'
```

### Budget ID

A budget's ID is assigned by SELECT when it is created, so there is nothing about your configuration that predicts it.

Open the budget in the SELECT UI and take the last segment of the URL, or list them with the API:

```bash
curl -s https://api.select.dev/v2/budgets \
  -H "Authorization: Bearer $SELECT_API_KEY" \
  -H "x-tenant-id: $SELECT_ORGANIZATION_ID" | jq '.items[] | {id, name, amount}'
```

## Converting Filter Expressions from JSON to Terraform

When you find a Usage Group in JSON mode, you can easily convert the `filter_expression` to Terraform configuration.

**JSON format (from SELECT UI):**
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

### Importing a Snowflake Account

**Command Format:**
```bash
terraform import select_snowflake_account.<resource_name> <snowflake_account_id>
```

**Example:**
```bash
terraform import select_snowflake_account.production acme-us-east-1
```

**Note**: An import cannot recover `credentials`. SELECT keeps them in a secret store and never returns them, so put the credentials in your configuration before importing. The first `terraform plan` after the import will show `credentials` as a change, and applying it re-sends them. That plan may also show a whitespace-only change to `excluded_users_filter_expression`, because SELECT stores it as a JSON document and re-serializes it on the way out; applying once settles it.

### Importing a Databricks Connection

**Command Format:**
```bash
terraform import select_databricks_connection.<resource_name> <databricks_connection_id>
```

**Example:**
```bash
terraform import select_databricks_connection.production 2f1c8b4e-9a6d-4d1f-9d0e-7d3a5b6c8e01
```

**Note**: An import cannot recover `credentials`. SELECT returns neither the client ID nor the secret on this resource, so put both in your configuration before importing. The first `terraform plan` after the import will show `credentials` as a change; applying it re-sends them, which makes SELECT revalidate the connection against Databricks.

### Importing a BigQuery Connection

**Command Format:**
```bash
terraform import select_bigquery_connection.<resource_name> <bigquery_connection_id>
```

**Example:**
```bash
terraform import select_bigquery_connection.production 2f1c8b4e-9a6d-4d1f-9d0e-7d3a5b6c8e01
```

**Note**: This resource treats `service_account` as required, but a connection reached without impersonation has none on the API side. Set one in your configuration before importing a connection like that, or the first `terraform plan` will show it as a change from null to a value.

### Importing an AWS Connection

**Command Format:**
```bash
terraform import select_aws_connection.<resource_name> <aws_connection_id>
```

**Example:**
```bash
terraform import select_aws_connection.production 2f1c8b4e-9a6d-4d1f-9d0e-7d3a5b6c8e01
```

**Note**: An import cannot recover `credentials`. SELECT returns neither the access key id nor the secret on this resource, so put both in your configuration before importing. The first `terraform plan` after the import will show `credentials` as a change; applying it re-sends them, which makes SELECT revalidate the connection against S3.

### Importing a Budget

**Command Format:**
```bash
terraform import select_budget.<resource_name> <budget_id>
```

**Example:**
```bash
terraform import select_budget.production 2f1c8b4e-9a6d-4d1f-9d0e-7d3a5b6c8e01
```

**Note**: Unlike the connection resources, a budget holds no secret, so nothing about the import is incomplete. `period` must still match what SELECT has on record — including `schedule_type` — or the first `terraform plan` after the import will show it as a change.

## Step-by-Step Import Process

### 1. Create Terraform Configuration

First, create your Terraform configuration file with the resource definitions:

```hcl
# Example: main.tf
terraform {
  required_providers {
    select = {
      source = "get-select/select"
    }
  }
}

provider "select" {
  api_key         = "your-api-key"
  organization_id = "your-org-id"
}

# Usage Group Set resource definition
resource "select_usage_group_set" "production_workloads" {
  name  = "Production Workloads"
  order = 1
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

### Issue: "Invalid Usage Group Import ID"

**Error Message:**
```
Error: Invalid Usage Group Import ID
Expected an import ID of the form `usage_group_set_id/usage_group_id`, got "<your_id>".
```

**Solution:** Ensure you're using the correct format with both IDs separated by a forward slash:
```bash
terraform import select_usage_group.my_group <usage_group_set_id>/<usage_group_id>
```

### Issue: "Cannot import non-existent remote object"

**Error Message:**
```
Error: Cannot import non-existent remote object

While attempting to import an existing object to "<resource_address>", the
provider detected that no object exists with the given id. Only pre-existing
objects can be imported; check that the id is correct and that it is
associated with the provider's configured region or endpoint, or use
"terraform apply" to create a new remote object for this resource.
```

This is Terraform's own message, not one this provider writes; it appears for
any resource type, not just usage groups. It means the id was well-formed but
nothing in SELECT matches it — for a usage group, both halves of the compound
id parsed, but the set, the group, or both do not exist under this API key's
organization.

**Solution:**
1. Double-check the ID(s) from the SELECT UI or the API
2. Verify the API key has access to the resource (see `x-tenant-id` and the
   key's scopes)
3. For a usage group, confirm the usage group set ID is correct first — an
   import against the wrong set 404s the same way

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
2. Verify your resource IDs are correct in the SELECT UI
3. Ensure your provider configuration (API key, organization ID) is correct
4. Contact your SELECT administrator for assistance with resource access 
