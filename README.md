# Select Terraform Provider

workflow comes from here
https://developer.hashicorp.com/terraform/plugin/code-generation/workflow-example

These are mieks notes as he got it working

u need to install go and terraform to work on this

internal is all codegened stuff, it's based off the public openapi spec

main.go is small batch homegrown code

## Development Setup

after installing gfo you should do
export GOPATH="$HOME/go/bin"
to set GOPATH which this project relies on

you also need to make a .terraformrc to override terraform for development.

`make setup-dev-overrides`

to configure this
```


if this is properly setup terraform plan will print this
```
│ Warning: Provider development overrides are in effect
...
```

## Importing Existing Resources

The Select Terraform provider supports importing existing resources that were created outside of Terraform. This allows you to bring existing infrastructure under Terraform management without recreating it.

**📖 See [IMPORT.md](./IMPORT.md) for detailed import instructions**

### Quick Reference:

**Usage Group Set:**
```bash
terraform import select_usage_group_set.<resource_name> <usage_group_set_id>
```

**Usage Group:**
```bash
terraform import select_usage_group.<resource_name> <usage_group_set_id>/<usage_group_id>
```

**Note:** You can find the required IDs in the Select UI under the Usage Groups section.

Usage Group Set Ids can be located in the query params when you select a usage group set, for example

`/app/<snowflake account uuid>/usage-groups/definitions?usageGroupSetId=<selected usage group set uuid>`

Usage Group id's can be found by switching from 'interactive' mode to JSON mode, for example:
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
This is also useful for copying the filter expression, for the above usage group, an equivalent block would be

```tf
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
## Development - Adding New Resources

The general pattern for adding resources is:
1. Extend public API with new CRUD routes that can manage the resource
2. Add new routes to `web/api/clients/generator_config.yml`
3. Run `make terraform-build` in the `web/api/clients` directory
 - This will update the `openapi.public.json` spec, and generate new internal types for the terraform provider
4. Run `tfplugingen-framework scaffold resource --name <new_resource_name> --output-dir ./terraform/internal`
- This will create the boilerplate for a new resource type, but it will not be connected to the codegened types we just created
5. Connect the generated types to the boilerplate code we just generated. This task should be one shot-able by whatever the flavour of the day LLM is
6. Add the new resource to the list of resources in `terraform/internal/provider.go`
7. Build the new terraform provider with `go install .` in `web/api/clients/terraform`
