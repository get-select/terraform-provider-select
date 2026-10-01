# SPDX-License-Identifier: MPL-2.0

"""
Terraform Resources Generator for SELECT Usage Groups

This script fetches existing usage group sets and usage groups from the SELECT API
and generates terraform configuration files and an import script.

Usage:
    python generate_usage_group_resources.py --token YOUR_TOKEN --org-id YOUR_ORG_ID
    python generate_usage_group_resources.py -t YOUR_TOKEN -o YOUR_ORG_ID

Usage Groups provide a flexible way of creating cost categories within SELECT.
You can learn more about usage groups in the SELECT documentation:
https://select.dev/docs/reference/using-select/usage-groups
"""

import requests
import json
import os
import re
import shlex
import argparse
from typing import Dict, List, Any


class SelectAPIClient:
    """Simple client for SELECT API operations."""

    def __init__(
        self,
        api_token: str,
        organization_id: str,
        base_url: str = "https://api.select.dev/"
    ):
        """
        Initialize the SELECT API client.

        Args:
            api_token: Bearer token for authentication
            organization_id: The organization ID
            base_url: Base URL for the SELECT API
        """
        self.base_url = base_url.rstrip("/") + "/v2/"
        # v2 scopes every request by this header rather than by an organization
        # id in the path.
        self.headers = {
            "Authorization": f"Bearer {api_token}",
            "x-tenant-id": organization_id,
        }

    def _list(self, endpoint: str) -> List[Dict[str, Any]]:
        """
        Read every page of a v2 collection.

        v2 answers a list with an envelope: the page in `items`, and a
        `page_token` to pass back for the next one. A null token means this was
        the last page.
        """
        items: List[Dict[str, Any]] = []
        params: Dict[str, str] = {}

        while True:
            response = requests.get(
                self.base_url + endpoint, headers=self.headers, params=params
            )
            response.raise_for_status()
            page = response.json()

            items.extend(page.get("items", []))

            token = page.get("page_token")
            if not token:
                return items
            params = {"page_token": token}

    def list_usage_group_sets(self) -> List[Dict[str, Any]]:
        """List all usage group sets."""
        return self._list("usage-group-sets")

    def list_usage_groups(self, usage_group_set_id: str) -> List[Dict[str, Any]]:
        """
        List all usage groups in a usage group set.

        Args:
            usage_group_set_id: The usage group set ID

        Returns:
            List of usage groups
        """
        return self._list(f"usage-group-sets/{usage_group_set_id}/usage-groups")


def sanitize_name(name: str) -> str:
    """Convert a name to a valid terraform resource name."""
    # Remove special characters and replace with underscores
    sanitized = re.sub(r'[^a-zA-Z0-9_]', '_', name)
    # Remove leading digits
    sanitized = re.sub(r'^[0-9]+', '', sanitized)
    # Ensure it starts with a letter or underscore
    if not sanitized or not sanitized[0].isalpha() and sanitized[0] != '_':
        sanitized = 'ug_' + sanitized
    return sanitized.lower()


def unique_resource_names(items: List[Dict[str, Any]]) -> Dict[str, str]:
    """
    Map each item's id to a terraform resource name unique within its file.

    Two usage groups can share a display name once their names differ only by
    characters sanitize_name strips, and two resources of the same type cannot
    share a name in one module. A collision is resolved by numbering, in the
    order the API returned them, so the output is stable between runs.
    """
    names: Dict[str, str] = {}
    used: Dict[str, int] = {}

    for item in items:
        base = sanitize_name(item['name'])
        seen = used.get(base, 0)
        used[base] = seen + 1
        names[item['id']] = base if seen == 0 else f"{base}_{seen + 1}"

    return names


def create_terraform_usage_group_set(usage_group_set: Dict[str, Any], resource_name: str) -> str:
    """
    Generate terraform configuration for a usage group set.

    Args:
        usage_group_set: Usage group set data from API
        resource_name: The terraform resource name to use

    Returns:
        Terraform configuration string
    """
    lines = [
        f'resource "select_usage_group_set" "{resource_name}" {{',
        f'  name  = {json.dumps(usage_group_set["name"])}',
        f'  order = {usage_group_set.get("order", 1)}',
    ]

    # Both are optional, and both default in a way that makes emitting them
    # unconditionally noisier than it is useful.
    if usage_group_set.get('team_id') is not None:
        lines.append(f'  team_id = {json.dumps(usage_group_set["team_id"])}')
    if usage_group_set.get('public'):
        lines.append('  public  = true')

    lines.append('}')
    return "\n".join(lines)


def create_terraform_usage_group(
    usage_group: Dict[str, Any],
    resource_name: str,
    usage_group_set_resource_name: str,
) -> str:
    """
    Generate terraform configuration for a usage group.

    Args:
        usage_group: Usage group data from API
        resource_name: The terraform resource name to use
        usage_group_set_resource_name: The terraform resource name for the parent usage group set

    Returns:
        Terraform configuration string
    """
    # The API returns the filter both structured and as a JSON string. The
    # string is what the provider takes, but it arrives compact — re-encoding it
    # through jsonencode keeps the generated configuration readable and is what
    # a person would have written by hand.
    filter_expression = json.loads(usage_group.get('filter_expression_json') or '{}')
    filter_json = json.dumps(filter_expression, indent=2).replace("\n", "\n  ")

    budget_line = ""
    if usage_group.get('budget') is not None:
        budget_line = f"  budget             = {usage_group['budget']}\n"

    return f'''resource "select_usage_group" "{resource_name}" {{
  name               = {json.dumps(usage_group['name'])}
  order              = {usage_group.get('order', 1)}
{budget_line}  usage_group_set_id = select_usage_group_set.{usage_group_set_resource_name}.id

  filter_expression_json = jsonencode({filter_json})
}}'''


def generate_import_statements(
    usage_group_sets: List[Dict[str, Any]],
    set_resource_names: Dict[str, str],
    all_usage_groups: Dict[str, List[Dict[str, Any]]],
    group_resource_names: Dict[str, Dict[str, str]],
) -> str:
    """
    Generate bash script with terraform import statements.

    Args:
        usage_group_sets: Every usage group set in the organization
        set_resource_names: Set id to terraform resource name
        all_usage_groups: Usage group set id to its usage groups
        group_resource_names: Set id to (group id to terraform resource name)

    Returns:
        Bash script content
    """
    script_lines = [
        "#!/bin/bash",
        "# Terraform import script for SELECT usage groups",
        "# Run this script from the directory holding the generated configuration.",
        "",
        "set -e",
        "",
        'echo "Importing SELECT usage groups..."',
        "",
    ]

    for usage_group_set in usage_group_sets:
        usage_group_set_id = usage_group_set['id']
        set_resource_name = set_resource_names[usage_group_set_id]

        set_label = shlex.quote(f'Importing usage group set: {usage_group_set["name"]}')
        script_lines.append(f'echo {set_label}')
        script_lines.append(
            f'terraform import select_usage_group_set.{set_resource_name} '
            f'{shlex.quote(usage_group_set_id)}'
        )

        for usage_group in all_usage_groups.get(usage_group_set_id, []):
            resource_name = group_resource_names[usage_group_set_id][usage_group['id']]
            group_label = shlex.quote(f'Importing usage group: {usage_group["name"]}')
            script_lines.append(f'echo {group_label}')
            # A usage group is addressed through its set on every route, so its
            # import address carries both ids.
            address = shlex.quote(f'{usage_group_set_id}/{usage_group["id"]}')
            script_lines.append(
                f'terraform import select_usage_group.{resource_name} {address}'
            )

        script_lines.append("")

    script_lines.extend([
        'echo "Import completed successfully!"',
        "echo \"You can now run 'terraform plan' to see any configuration drift.\"",
    ])

    return "\n".join(script_lines)


def generate_main_tf(organization_id: str, api_key: str) -> str:
    """
    Generate main.tf with the provider configuration.

    Args:
        organization_id: The organization ID
        api_key: The API key to hardcode

    Returns:
        Terraform main.tf content
    """
    return f'''# SELECT Terraform Provider Configuration
#
# SECURITY WARNING: This file contains a hardcoded API key!
# The API key is sensitive information and should be stored securely.
# Consider using environment variables, terraform.tfvars (in .gitignore),
# or a secret management system for production use.

terraform {{
  required_providers {{
    select = {{
      source = "get-select/select"
    }}
  }}
}}

provider "select" {{
  # TODO SECURITY WARNING: Move this api key to a secure location
  api_key         = "{api_key}"
  organization_id = "{organization_id}"
}}
'''


def main():
    """
    Main function to generate terraform configurations and import scripts.
    """
    parser = argparse.ArgumentParser(
        description='Generate Terraform configurations for SELECT usage groups',
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""
Examples:
  python generate_usage_group_resources.py --token YOUR_TOKEN --org-id YOUR_ORG_ID
  python generate_usage_group_resources.py -t YOUR_TOKEN -o YOUR_ORG_ID
        """
    )

    parser.add_argument(
        '--token', '-t',
        required=True,
        help='SELECT API token (Bearer token for authentication)'
    )

    parser.add_argument(
        '--org-id', '-o',
        required=True,
        dest='organization_id',
        help='SELECT organization ID'
    )

    parser.add_argument(
        '--base-url',
        default='https://api.select.dev/',
        help='Base URL for the SELECT API (default: https://api.select.dev/)'
    )

    parser.add_argument(
        '--output-dir',
        default='select_usage_groups',
        help='Output directory for generated files (default: select_usage_groups)'
    )

    args = parser.parse_args()

    api_token = args.token
    organization_id = args.organization_id

    client = SelectAPIClient(api_token, organization_id, args.base_url)

    try:
        print("Fetching usage group sets...")
        usage_group_sets = client.list_usage_group_sets()

        if not usage_group_sets:
            print("This organization has no usage group sets; nothing to generate.")
            return

        set_resource_names = unique_resource_names(usage_group_sets)

        print("Fetching usage groups...")
        all_usage_groups: Dict[str, List[Dict[str, Any]]] = {}
        group_resource_names: Dict[str, Dict[str, str]] = {}

        for usage_group_set in usage_group_sets:
            usage_group_set_id = usage_group_set['id']
            usage_groups = client.list_usage_groups(usage_group_set_id)
            all_usage_groups[usage_group_set_id] = usage_groups
            group_resource_names[usage_group_set_id] = unique_resource_names(usage_groups)

        os.makedirs(args.output_dir, exist_ok=True)

        # One file per usage group set, holding the set and the groups in it.
        for usage_group_set in usage_group_sets:
            usage_group_set_id = usage_group_set['id']
            set_resource_name = set_resource_names[usage_group_set_id]
            filename = f"{args.output_dir}/{set_resource_name}.tf"

            with open(filename, 'w') as f:
                f.write("# Usage Group Set\n")
                f.write(create_terraform_usage_group_set(usage_group_set, set_resource_name))
                f.write("\n\n")

                usage_groups = all_usage_groups.get(usage_group_set_id, [])
                if usage_groups:
                    f.write("# Usage Groups\n")
                    for usage_group in usage_groups:
                        resource_name = group_resource_names[usage_group_set_id][usage_group['id']]
                        f.write(create_terraform_usage_group(
                            usage_group, resource_name, set_resource_name
                        ))
                        f.write("\n\n")

            print(f"Generated {filename}")

        import_script = generate_import_statements(
            usage_group_sets, set_resource_names, all_usage_groups, group_resource_names
        )
        import_script_path = f"{args.output_dir}/import.sh"
        with open(import_script_path, 'w') as f:
            f.write(import_script)
        os.chmod(import_script_path, 0o755)
        print(f"Generated {import_script_path}")

        main_tf_path = f"{args.output_dir}/main.tf"
        with open(main_tf_path, 'w') as f:
            f.write(generate_main_tf(organization_id, api_token))
        print(f"Generated {main_tf_path}")

        total_groups = sum(len(groups) for groups in all_usage_groups.values())
        print("\nGeneration complete!")
        print(f"Generated {len(usage_group_sets)} usage group set(s) and {total_groups} usage group(s).")

        print("\nNext steps:")
        print(f"\t0. Optionally move the directory {args.output_dir} into your existing terraform project")
        print("\t1. WARNING: the API key you provided is hardcoded in main.tf! It should be stored securely.")
        print(f"\t2. cd {args.output_dir}")
        print("\t3. Run 'terraform fmt' to lay the generated configuration out canonically")
        print("\t4. Run 'terraform init'")
        print("\t5. Run './import.sh' to import existing resources")
        print("\t6. Run 'terraform plan', there may be some discrepancies between the existing resources and the terraform configuration")
        print("\t7. Update the terraform configuration to match the existing resources, until 'terraform plan' shows no discrepancies")

    except requests.exceptions.RequestException as e:
        print(f"API Error: {e}")
    except Exception as e:
        print(f"Error: {e}")


if __name__ == "__main__":
    main()
