# SPDX-License-Identifier: MPL-2.0

"""
Terraform Resources Generator for SELECT Usage Groups

This script fetches existing usage group sets and usage groups from the SELECT API
and generates terraform configuration files and import scripts organized by Snowflake account.

Usage:
    python generate_terraform_resources.py --token YOUR_TOKEN --org-id YOUR_ORG_ID
    python generate_terraform_resources.py -t YOUR_TOKEN -o YOUR_ORG_ID

Usage Groups provide a flexible way of creating cost categories within SELECT.
You can learn more about usage groups in the SELECT documentation:
https://select.dev/docs/reference/using-select/usage-groups
"""

import requests
import json
import os
import re
import argparse
from typing import Dict, List, Optional, Any
from collections import defaultdict


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
        self.base_url = base_url.rstrip("/") + "/"
        self.organization_id = organization_id
        self.headers = {"Authorization": f"Bearer {api_token}"}

    def _make_request(self, method: str, endpoint: str, **kwargs) -> requests.Response:
        """Make a request to the SELECT API."""
        url = self.base_url + endpoint
        response = requests.request(method, url, headers=self.headers, **kwargs)
        response.raise_for_status()
        return response

    def list_usage_group_sets(self) -> List[Dict[str, Any]]:
        """
        List all usage group sets.
        """
        endpoint = f"api/{self.organization_id}/usage-group-sets"
        response = self._make_request("GET", endpoint)
        return response.json()

    def list_usage_groups(
        self, usage_group_set_id: str
    ) -> List[Dict[str, Any]]:
        """
        List all usage groups in a usage group set.

        Args:
            usage_group_set_id: The usage group set ID

        Returns:
            List of usage groups
        """
        endpoint = (
            f"api/{self.organization_id}/usage-group-sets/{usage_group_set_id}/usage-groups"
        )
        response = self._make_request("GET", endpoint)
        return response.json()


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


def get_account_identifier(usage_group_set: Dict[str, Any]) -> str:
    """
    Get a unique identifier for the Snowflake account/organization.
    
    Args:
        usage_group_set: Usage group set data from API
        
    Returns:
        A sanitized identifier for the account/organization
    """
    if usage_group_set.get('snowflake_account_uuid') is not None:
        # Use the UUID as a more stable identifier
        account_uuid = usage_group_set['snowflake_account_uuid']
        # Take first 8 characters of UUID for readability
        return f"account_{account_uuid[:8]}"
    else:
        # Use organization name
        org_name = usage_group_set['snowflake_organization_name']
        return f"org_{sanitize_name(org_name)}"


def create_terraform_usage_group_set(usage_group_set: Dict[str, Any]) -> str:
    """
    Generate terraform configuration for a usage group set.
    
    Args:
        usage_group_set: Usage group set data from API
        
    Returns:
        Terraform configuration string
    """
    resource_name = sanitize_name(usage_group_set['name'])
    ownership_line = ""

    if usage_group_set.get('snowflake_account_uuid') is not None:
        ownership_line = f'snowflake_account_uuid = "{usage_group_set["snowflake_account_uuid"]}"'
    else:
        ownership_line = f'snowflake_organization_name = "{usage_group_set["snowflake_organization_name"]}"'
    terraform_config = f'''resource "select_usage_group_set" "{resource_name}" {{
  name  = "{usage_group_set['name']}"
  order = {usage_group_set.get('order', 1)}
  {ownership_line}
}}'''
    
    return terraform_config


def create_terraform_usage_group(usage_group: Dict[str, Any], usage_group_set_resource_name: str) -> str:
    """
    Generate terraform configuration for a usage group.
    
    Args:
        usage_group: Usage group data from API
        usage_group_set_resource_name: The terraform resource name for the parent usage group set
        
    Returns:
        Terraform configuration string
    """
    resource_name = sanitize_name(usage_group['name'])
    
    # Handle filter expression
    filter_json = json.dumps(usage_group.get('filter_expression', {}), indent=2)
    
    # Format budget
    budget_line = ""
    if usage_group.get('budget') is not None:
        budget_line = f"  budget = {usage_group['budget']}\n"
    
    terraform_config = f'''resource "select_usage_group" "{resource_name}" {{
  name               = "{usage_group['name']}"
  order              = {usage_group.get('order', 1)}
{budget_line}  usage_group_set_id = select_usage_group_set.{usage_group_set_resource_name}.id
  
  filter_expression_json = jsonencode({filter_json})
}}'''
    
    return terraform_config


def generate_import_statements(grouped_usage_group_sets: Dict[str, List[Dict[str, Any]]], all_usage_groups: Dict[str, List[Dict[str, Any]]]) -> str:
    """
    Generate bash script with terraform import statements for modular structure.
    
    Args:
        grouped_usage_group_sets: Dictionary mapping account identifiers to their usage group sets
        all_usage_groups: Dictionary mapping usage group set IDs to their usage groups
        
    Returns:
        Bash script content
    """
    script_lines = [
        "#!/bin/bash",
        "# Terraform import script for SELECT usage groups (modular structure)",
        "# Run this script from the root directory after applying the terraform configuration",
        "",
        "set -e",
        "",
        "echo \"Importing SELECT usage groups...\"",
        ""
    ]
    
    # Import for each account module
    for account_id, usage_group_sets in grouped_usage_group_sets.items():
        script_lines.append(f'echo "Importing resources for {account_id}..."')
        script_lines.append("")
        
        # Import usage group sets for this account
        for usage_group_set in usage_group_sets:
            resource_name = sanitize_name(usage_group_set['name'])
            usage_group_set_id = usage_group_set['id']
            script_lines.append(f'echo "Importing usage group set: {usage_group_set["name"]}"')
            script_lines.append(f'terraform import module.{account_id}.select_usage_group_set.{resource_name} {usage_group_set_id}')
        
        script_lines.append("")
        
        # Import usage groups for this account
        for usage_group_set in usage_group_sets:
            usage_group_set_resource_name = sanitize_name(usage_group_set['name'])
            usage_group_set_id = usage_group_set['id']
            
            if usage_group_set_id in all_usage_groups:
                for usage_group in all_usage_groups[usage_group_set_id]:
                    resource_name = sanitize_name(usage_group['name'])
                    usage_group_id = usage_group['id']
                    script_lines.append(f'echo "Importing usage group: {usage_group["name"]}"')
                    script_lines.append(f'terraform import module.{account_id}.select_usage_group.{resource_name} {usage_group_set_id}/{usage_group_id}')
        
        script_lines.append("")
    
    script_lines.extend([
        "echo \"Import completed successfully!\"",
        "echo \"You can now run 'terraform plan' to see any configuration drift.\""
    ])
    
    return "\n".join(script_lines)


def generate_main_tf(organization_id: str, api_key: str, grouped_usage_group_sets: Dict[str, List[Dict[str, Any]]]) -> str:
    """
    Generate main.tf file with provider configuration and module calls.
    
    Args:
        organization_id: The organization ID
        api_key: The API key to hardcode
        grouped_usage_group_sets: Dictionary mapping account identifiers to their usage group sets
        
    Returns:
        Terraform main.tf content
    """
    # Generate module calls
    module_calls = []
    for account_id, usage_group_sets in grouped_usage_group_sets.items():
        # Get account details from first usage group set (they're all the same account)
        first_set = usage_group_sets[0]
        if first_set.get('snowflake_account_uuid'):
            account_info = f"Snowflake Account UUID: {first_set['snowflake_account_uuid']}"
        else:
            account_info = f"Snowflake Organization: {first_set['snowflake_organization_name']}"
        
        module_call = f'''# {account_info}
module "{account_id}" {{
  source = "./{account_id}"
}}'''
        module_calls.append(module_call)
    
    modules_section = "\n\n".join(module_calls)
    
    main_tf_content = f'''# SELECT Terraform Provider Configuration
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

# Usage Group Modules by Snowflake Account/Organization

{modules_section}
'''
    
    return main_tf_content


def generate_module_main_tf() -> str:
    """
    Generate main.tf file for each module directory.
    
    Returns:
        Terraform module main.tf content
    """
    return '''# This module inherits the provider configuration from the root module
terraform {
  required_providers {
    select = {
      source = "get-select/select"
    }
  }
}
'''


def main():
    """
    Main function to generate terraform configurations and import scripts.
    """
    parser = argparse.ArgumentParser(
        description='Generate Terraform configurations for SELECT usage groups (modular structure)',
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""
Examples:
  python generate_terraform_resources.py --token YOUR_TOKEN --org-id YOUR_ORG_ID
  python generate_terraform_resources.py -t YOUR_TOKEN -o YOUR_ORG_ID
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
    
    # Initialize client
    client = SelectAPIClient(api_token, organization_id, args.base_url)
    
    try:
        # Fetch usage group sets
        print("Fetching usage group sets...")
        usage_group_sets = client.list_usage_group_sets()
        
        # Group usage group sets by Snowflake account/organization
        grouped_usage_group_sets = defaultdict(list)
        for usage_group_set in usage_group_sets:
            account_id = get_account_identifier(usage_group_set)
            grouped_usage_group_sets[account_id].append(usage_group_set)
        
        # Fetch usage groups for each set
        print("Fetching usage groups...")
        all_usage_groups = {}
        
        for usage_group_set in usage_group_sets:
            usage_group_set_id = usage_group_set['id']
            usage_groups = client.list_usage_groups(usage_group_set_id)
            all_usage_groups[usage_group_set_id] = usage_groups
        
        # Create output directory structure
        os.makedirs(args.output_dir, exist_ok=True)
        
        # Generate terraform files for each account module
        for account_id, account_usage_group_sets in grouped_usage_group_sets.items():
            module_dir = f"{args.output_dir}/{account_id}"
            os.makedirs(module_dir, exist_ok=True)
            
            # Generate module main.tf
            module_main_path = f"{module_dir}/main.tf"
            with open(module_main_path, 'w') as f:
                f.write(generate_module_main_tf())
            print(f"Generated {module_main_path}")
            
            # Generate usage group sets and groups for this account
            for usage_group_set in account_usage_group_sets:
                usage_group_set_resource_name = sanitize_name(usage_group_set['name'])
                filename = f"{module_dir}/{usage_group_set_resource_name}.tf"
                
                with open(filename, 'w') as f:
                    # Write usage group set
                    f.write("# Usage Group Set\n")
                    f.write(create_terraform_usage_group_set(usage_group_set))
                    f.write("\n\n")
                    
                    # Write usage groups
                    usage_group_set_id = usage_group_set['id']
                    if usage_group_set_id in all_usage_groups:
                        f.write("# Usage Groups\n")
                        for usage_group in all_usage_groups[usage_group_set_id]:
                            f.write(create_terraform_usage_group(usage_group, usage_group_set_resource_name))
                            f.write("\n\n")
                
                print(f"Generated {filename}")
        
        # Generate import script
        import_script = generate_import_statements(grouped_usage_group_sets, all_usage_groups)
        import_script_path = f"{args.output_dir}/import.sh"
        with open(import_script_path, 'w') as f:
            f.write(import_script)
        
        # Make import script executable
        os.chmod(import_script_path, 0o755)
        
        print(f"Generated {import_script_path}")

        # Generate main.tf with module calls
        main_tf_content = generate_main_tf(organization_id, api_token, grouped_usage_group_sets)
        main_tf_path = f"{args.output_dir}/main.tf"
        with open(main_tf_path, 'w') as f:
            f.write(main_tf_content)
        print(f"Generated {main_tf_path}")
        
        print("\nGeneration complete!")
        print(f"Generated modular structure with {len(grouped_usage_group_sets)} Snowflake account/organization modules:")
        for account_id, sets in grouped_usage_group_sets.items():
            print(f"  - {account_id}: {len(sets)} usage group set(s)")
        
        print("\nNext steps:")
        print(f"\t0. Optionally move the directory {args.output_dir} into your existing terraform project")
        print("\t1. WARNING: the API key you provided is hardcoded in main.tf! It should be stored securely.")
        print(f"\t2. cd {args.output_dir}")
        print("\t3. Run 'terraform init'")
        print("\t4. Run './import.sh' to import existing resources")
        print("\t5. Run 'terraform plan', there may be some discrepancies between the existing resources and the terraform configuration")
        print("\t6. Update the terraform configuration to match the existing resources, until 'terraform plan' shows no discrepancies")
        
    except requests.exceptions.RequestException as e:
        print(f"API Error: {e}")
    except Exception as e:
        print(f"Error: {e}")


if __name__ == "__main__":
    main()
