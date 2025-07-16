# SPDX-License-Identifier: MPL-2.0

"""
Terraform Resources Generator for SELECT Usage Groups

This script fetches existing usage group sets and usage groups from the SELECT API
and generates terraform configuration files and import scripts.

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


def create_terraform_usage_group_set(usage_group_set: Dict[str, Any]) -> str:
    """
    Generate terraform configuration for a usage group set.
    
    Args:
        usage_group_set: Usage group set data from API
        
    Returns:
        Terraform configuration string
    """
    resource_name = sanitize_name(usage_group_set['name'])
    
    terraform_config = f'''resource "select_usage_group_set" "{resource_name}" {{
  name  = "{usage_group_set['name']}"
  order = {usage_group_set.get('order', 1)}
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


def generate_import_statements(usage_group_sets: List[Dict[str, Any]], all_usage_groups: Dict[str, List[Dict[str, Any]]]) -> str:
    """
    Generate bash script with terraform import statements.
    
    Args:
        usage_group_sets: List of usage group sets
        all_usage_groups: Dictionary mapping usage group set IDs to their usage groups
        
    Returns:
        Bash script content
    """
    script_lines = [
        "#!/bin/bash",
        "# Terraform import script for SELECT usage groups",
        "# Run this script after applying the terraform configuration",
        "",
        "set -e",
        "",
        "echo \"Importing SELECT usage groups...\"",
        ""
    ]
    
    # Import usage group sets
    for usage_group_set in usage_group_sets:
        resource_name = sanitize_name(usage_group_set['name'])
        usage_group_set_id = usage_group_set['id']
        script_lines.append(f'echo "Importing usage group set: {usage_group_set["name"]}"')
        script_lines.append(f'terraform import select_usage_group_set.{resource_name} {usage_group_set_id}')
    
    script_lines.append("")
    
    # Import usage groups
    for usage_group_set in usage_group_sets:
        usage_group_set_resource_name = sanitize_name(usage_group_set['name'])
        usage_group_set_id = usage_group_set['id']
        
        if usage_group_set_id in all_usage_groups:
            for usage_group in all_usage_groups[usage_group_set_id]:
                resource_name = sanitize_name(usage_group['name'])
                usage_group_id = usage_group['id']
                script_lines.append(f'echo "Importing usage group: {usage_group["name"]}"')
                script_lines.append(f'terraform import select_usage_group.{resource_name} {usage_group_set_id}/{usage_group_id}')
    
    script_lines.extend([
        "",
        "echo \"Import completed successfully!\"",
        "echo \"You can now run 'terraform plan' to see any configuration drift.\""
    ])
    
    return "\n".join(script_lines)


def generate_main_tf(organization_id: str, api_key: str) -> str:
    """
    Generate main.tf file with provider configuration and security warnings.
    
    Args:
        organization_id: The organization ID
        api_key: The API key to hardcode
        
    Returns:
        Terraform main.tf content
    """
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
'''
    
    return main_tf_content


def main():
    """
    Main function to generate terraform configurations and import scripts.
    """
    parser = argparse.ArgumentParser(
        description='Generate Terraform configurations for SELECT usage groups',
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
        
        # Fetch usage groups for each set
        print("Fetching usage groups...")
        all_usage_groups = {}
        
        for usage_group_set in usage_group_sets:
            usage_group_set_id = usage_group_set['id']
            usage_groups = client.list_usage_groups(usage_group_set_id)
            all_usage_groups[usage_group_set_id] = usage_groups
        
        # Generate terraform files for each usage group set
        os.makedirs(args.output_dir, exist_ok=True)
        
        for usage_group_set in usage_group_sets:
            usage_group_set_resource_name = sanitize_name(usage_group_set['name'])
            filename = f"{args.output_dir}/{usage_group_set_resource_name}.tf"
            
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
        import_script = generate_import_statements(usage_group_sets, all_usage_groups)
        import_script_path = f"{args.output_dir}/import.sh"
        with open(import_script_path, 'w') as f:
            f.write(import_script)
        
        # Make import script executable
        os.chmod(import_script_path, 0o755)
        
        print(f"Generated {import_script_path}")

        # Generate main.tf
        main_tf_content = generate_main_tf(organization_id, api_token)
        main_tf_path = f"{args.output_dir}/main.tf"
        with open(main_tf_path, 'w') as f:
            f.write(main_tf_content)
        print(f"Generated {main_tf_path}")
        
        print("\nGeneration complete!")
        print("Next steps:")
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
