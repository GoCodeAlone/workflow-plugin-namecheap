// Package internal — gRPC plugin bootstrap.
//
// The plugin entrypoint is cmd/workflow-plugin-namecheap/main.go which calls
// sdk.ServeIaCPlugin(internal.NewIaCServer(), sdk.IaCServeOptions{}).
// All gRPC logic lives in iacserver.go.
package internal
