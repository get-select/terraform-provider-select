.PHONY: codegen build install clean reset test test-all test-validate test-clean setup-dev-overrides docs
codegen-go:
	mkdir -p ./internal/provider
	tfplugingen-openapi generate \
		--config generator_config.yml \
		--output ./internal/provider/provider_code_spec.json \
		openapi.public.json
	tfplugingen-framework generate all \
		--input ./internal/provider/provider_code_spec.json \
		--output ./internal/provider

codegen:
	@echo "Fetching OpenAPI spec from public API..."
	curl -s -o openapi.public.json https://api.select.dev/public_openapi
	@echo "OpenAPI spec downloaded successfully"
	make codegen-go
build:
	@echo "Building provider..."
	go mod tidy
	go build ./...
	@echo "Build complete!"
install: build
	@echo "Installing provider..."
	go install .
	@echo "Install complete!"
clean:
	@echo "Cleaning Terraform state files..."
	rm -f terraform.tfstate terraform.tfstate.backup
	@echo "Cleaning Go build cache..."
	go clean -cache
	@echo "Cleaning Go installed packages..."
	go clean -i ./... || true
	@echo "Cleaning generated provider code..."
	rm -rf ./internal/provider/
	@echo "Cleaning downloaded OpenAPI spec..."
	rm -f openapi.public.json
	@echo "Tidying Go modules..."
	go mod tidy
	@echo "Clean complete!"
reset: clean codegen install
	@echo "Complete reset finished! Provider rebuilt with latest changes."

# Setup dev overrides for local development
setup-dev-overrides:
	@echo "Setting up Terraform dev overrides..."
	@mkdir -p ~/.terraform.d
	cp example.terraformrc ~/.terraform.d/.terraformrc
	@if [ "$$(uname)" = "Darwin" ]; then \
		sed -i '' "s|{{GO_PATH_BIN}}|$$HOME/go/bin|g" ~/.terraform.d/.terraformrc; \
	else \
		sed -i "s|{{GO_PATH_BIN}}|$$HOME/go/bin|g" ~/.terraform.d/.terraformrc; \
	fi
	@echo "Dev overrides configured in ~/.terraform.d/.terraformrc"
	cat ~/.terraform.d/.terraformrc


# Testing targets
test-validate:
	@echo "Validating test configurations..."
	# Critical: Dev overrides require TF_CLI_CONFIG_FILE to be set in CI environments
	# The terraform validate command will show a warning about dev overrides when working correctly
	@cd tests && terraform validate
	@echo "Test configuration validation complete!"


test-all:
	@echo "Running all Terraform provider tests..."
	@echo "========================================="
	@echo "Running provider tests..."
	@echo "Note: Skipping terraform init when using dev overrides (as recommended by Terraform)"
	cd tests && terraform test provider.tftest.hcl
	@echo "========================================="
	@echo "All tests completed!"

test-clean:
	@echo "Cleaning up test resources..."
	@echo "Removing test state files..."
	find tests/ -name "terraform.tfstate*" -delete
	find tests/ -name ".terraform" -type d -exec rm -rf {} + 2>/dev/null || true
	find tests/ -name ".terraform.lock.hcl" -delete
	@echo "Test cleanup complete!"

# Convenience alias
test: test-all

docs:
	@echo "Generating provider schema and documentation..."
	terraform providers schema -json | sed 's/"hashicorp.com\/edu\/select"/"select"/g' > providers-schema.json
	tfplugindocs generate --provider-name=select --providers-schema=providers-schema.json
	rm providers-schema.json


# Help target
help:
	@echo "Available targets:"
	@echo "  codegen          - Generate provider code from OpenAPI spec"
	@echo "  build            - Build the provider"
	@echo "  install          - Install the provider locally"
	@echo "  setup-dev-overrides - Setup Terraform dev overrides for local development"
	@echo "  clean            - Clean build artifacts and state files"
	@echo "  reset            - Complete reset: clean, codegen, and install"
	@echo ""
	@echo "Testing targets:"
	@echo "  test-validate    - Validate test configuration syntax"
	@echo "  test-all         - Run all tests (alias: test)"
	@echo "  test-clean       - Clean up test resources and state files"
	@echo ""
	@echo "Run individual tests with:"
	@echo "  terraform test tests/provider.tftest.hcl"
	@echo "Or run specific test cases with:"
	@echo "  terraform test tests/provider.tftest.hcl -filter=create_usage_group_set"
	@echo ""
	@echo "  help             - Show this help message"

