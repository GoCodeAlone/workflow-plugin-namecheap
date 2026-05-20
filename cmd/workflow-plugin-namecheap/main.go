// Command workflow-plugin-namecheap is a workflow IaC plugin that
// implements the `infra.dns` resource type against Namecheap.
//
// It runs as a gRPC subprocess via go-plugin, registered via
// sdk.ServeIaCPlugin. The plugin declares ComputePlanVersion="v2"
// in its Capabilities response so wfctl routes all plan/apply
// operations through the typed v2 dispatch path.
package main

import (
	"github.com/GoCodeAlone/workflow-plugin-namecheap/internal"
	sdk "github.com/GoCodeAlone/workflow/plugin/external/sdk"
)

func main() {
	sdk.ServeIaCPlugin(internal.NewIaCServer(), sdk.IaCServeOptions{})
}
