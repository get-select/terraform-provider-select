# Terraform Provider for Select - Development

This repository contains the source code for the [Select Terraform Provider](https://registry.terraform.io/providers/get-select/select). This provider is _mostly_ **auto-generated** from Select's public OpenAPI specification, enabling developers to manage Select platform resources through Terraform.

> **For usage documentation and examples**, visit the [official Terraform Registry documentation](https://registry.terraform.io/providers/get-select/select/latest/docs).

## About Select

[Select](https://select.dev) helps organizations optimize their Snowflake usage and costs. This Terraform provider enables Infrastructure as Code management of Select platform resources like usage groups and usage group sets.

## Architecture Overview

This provider is built using:
- **[Terraform Plugin Framework](https://github.com/hashicorp/terraform-plugin-framework)** - Modern Terraform provider development
- **[tfplugingen-openapi](https://github.com/hashicorp/terraform-plugin-codegen-openapi)** - OpenAPI to Terraform schema generation  
- **[tfplugingen-framework](https://github.com/hashicorp/terraform-plugin-codegen-framework)** - Framework code generation
- **Select's Public OpenAPI Spec** - Single source of truth hosted at `https://api.select.dev/public_openapi`

### Code Generation Workflow

The provider code is generated from Select's public OpenAPI specification:

1. **Fetch OpenAPI Spec**: Downloads the latest spec from `https://api.select.dev/public_openapi`
2. **Generate Schema**: `tfplugingen-openapi` converts OpenAPI spec to Terraform schema definitions
3. **Generate Code**: `tfplugingen-framework` creates the final provider code
4. **Manual Customization**: Configuration in `generator_config.yml` allows for customizations and overrides

This ensures the provider stays in sync with Select's API automatically.

## Development Requirements

- **[Go](https://golang.org/doc/install)** >= 1.24.4
- **[Terraform](https://www.terraform.io/downloads.html)** >= 1.7.0

### Required Tools

The following tools are automatically installed during the build process:

```bash
# These are installed automatically by the Makefile
go install github.com/hashicorp/terraform-plugin-codegen-openapi/cmd/tfplugingen-openapi@latest
go install github.com/hashicorp/terraform-plugin-codegen-framework/cmd/tfplugingen-framework@latest
```

## Quick Start

```bash
# Clone the repository
git clone https://github.com/get-select/terraform-provider-select
cd terraform-provider-select

# Generate provider code from OpenAPI spec and build
make reset

# Set up local development
make setup-dev-overrides
```

## Development Commands

| Command | Description |
|---------|-------------|
| `make help` | Show all available commands |
| `make codegen` | Download OpenAPI spec and generate provider code |
| `make build` | Build the provider binary |
| `make install` | Install provider locally for testing |
| `make setup-dev-overrides` | Configure Terraform to use local provider |
| `make test` | Run provider tests |
| `make clean` | Remove all generated files and build artifacts |
| `make reset` | Full reset: clean, regenerate, and install |

### Development Workflow

1. **Initial Setup**:
   ```bash
   make reset                    # Generate code and build
   make setup-dev-overrides     # Configure local development overrides
   ```

2. **Making Changes**:
   ```bash
   # After modifying generator_config.yml or when API updates:
   make reset
   
   # For code-only changes (if manually editing generated code):
   make build install
   ```

  Types from the API spec are added to `internal/provider/`. Some boilerplate is then needed to connect those types to the Api in a way that terraform understands. There is one of these per resource in `internal/`.

3. **Testing**:

  Testign requires and API key and organization generated from teh SELECT backend you wish to test against. You can generate an API key in Settings -> API Keys and you can find your organization id in Settings -> Profile.
   ```bash
   # Set required environment variables
   export TF_VAR_select_api_key="your-api-key"
   export TF_VAR_select_organization_id="your-org-id"
   
   # Run tests
   make test
   ```

## Configuration Files

### `generator_config.yml`
Configures the code generation process:
- Resource mappings (API endpoints to Terraform resources)
- Schema overrides and customizations
- Field descriptions and validation rules
- Ignored fields that don't map well to Terraform

### `example.terraformrc`
Template for Terraform development overrides that allows using the locally built provider instead of downloading from the registry.

## Project Structure

```
terraform-provider-select/
├── internal/                    # Hand-written provider core
│   ├── provider.go             # Provider configuration and setup
│   ├── api.go                  # HTTP client and API utilities
│   ├── usage_group_resource.go # Custom resource implementations
│   └── provider/               # Generated code (git-ignored)
├── tests/                      # Provider tests
├── docs/                       # Generated documentation
├── examples/                   # Usage examples
├── generator_config.yml        # Code generation configuration
├── Makefile                    # Development commands
└── main.go                     # Provider entry point
```

## OpenAPI Dependency

**Important**: This provider is entirely dependent on Select's public OpenAPI specification. The specification is:

- **Hosted at**: `https://api.select.dev/public_openapi`
- **Auto-fetched**: Every `make codegen` downloads the latest spec
- **Single Source of Truth**: Changes to the API automatically reflect in the provider

If the OpenAPI endpoint is unavailable, the code generation will fail. For offline development, you can work with a previously downloaded `openapi.public.json` file.

## Testing

The provider includes comprehensive tests that validate functionality against the live Select API:

```bash
# Run all tests
make test

# Run specific test files  
cd tests && terraform test provider.tftest.hcl

# Run specific test cases
cd tests && terraform test provider.tftest.hcl -filter=create_usage_group_set
```

**Note**: Tests require valid Select API credentials and will create/modify real resources.

## Documentation Generation

Documentation is auto-generated from the provider schema:

```bash
make docs
```

This creates documentation in the `docs/` directory that matches the format used in the Terraform Registry.

## Release Process

Releases are automated via GitHub Actions when tags are pushed:

1. Code is generated from the latest OpenAPI spec
2. Provider is built for multiple platforms
3. Binaries are signed and uploaded to GitHub releases
4. Terraform Registry is automatically updated

## Contributing

1. **Fork** the repository
2. **Create a feature branch** from `main`
3. **Make your changes** to `generator_config.yml` or core provider files
4. **Test your changes** with `make test`
5. **Submit a pull request**

### Common Development Tasks

**Adding a new resource**:
1. Update `generator_config.yml` with the new resource configuration
2. Run `make reset` to regenerate code
3. Add any custom logic in `internal/`
4. Add tests in `tests/`

**Modifying existing resources**:
1. Update the relevant section in `generator_config.yml`
2. Run `make reset`
3. Test changes with `make test`

## Troubleshooting

**"Provider not found" errors**:
```bash
make setup-dev-overrides  # Ensure dev overrides are configured
```

**Code generation failures**:
```bash
# Check internet connection and try again
make clean && make codegen
```

**Test failures**:
```bash
# Ensure credentials are set
export TF_VAR_select_api_key="your-key"
export TF_VAR_select_organization_id="your-org-id"

# Clean test state
make test-clean && make test
```

## Links

- **[Terraform Registry](https://registry.terraform.io/providers/get-select/select/latest)** - Official provider documentation and usage examples
- **[Select Platform](https://select.dev)** - Select platform documentation
- **[Provider Issues](https://github.com/get-select/terraform-provider-select/issues)** - Report bugs or request features
- **[Terraform Plugin Framework](https://developer.hashicorp.com/terraform/plugin/framework)** - Framework documentation

## License

This project is licensed under the Mozilla Public License 2.0 - see the [LICENSE](LICENSE) file for details.
