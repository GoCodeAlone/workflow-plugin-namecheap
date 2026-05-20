// Command workflow-plugin-namecheap is a workflow IaC plugin that
// implements the `infra.dns` resource type against Namecheap.
//
// Run by the workflow engine as a gRPC subprocess via go-plugin.
package main

import (
	"github.com/GoCodeAlone/workflow-plugin-namecheap/internal"
)

func main() {
	internal.Serve()
}
