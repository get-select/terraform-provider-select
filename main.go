// SPDX-License-Identifier: MPL-2.0

package main

import (
	"context"
	"log"
	provider "terraform-provider-select/internal"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
)

func main() {
	opts := providerserver.ServeOpts{
		Address: "registry.terraform.io/get-select/select",
	}

	err := providerserver.Serve(context.Background(), provider.New(), opts)
	if err != nil {
		log.Fatal(err.Error())
	}
}
