package internal

// Serve is the gRPC plugin entry-point. Future work: register the
// Driver via the workflow IaC ResourceDriver gRPC service per
// workflow's plugin/external/iac protocol.
//
// Initial scaffold stub: prints capability info and exits. Full
// gRPC plumbing lands in a follow-up commit once the upstream IaC
// SDK shape stabilises (see workflow#640 Phase 3 — typed IaC
// driver registration).
func Serve() {
	// Placeholder. Real implementation:
	//
	//   sdk.ServeIaCProvider(internal.iacAdapter{driver: d})
	//
	// is deferred until the IaC SDK exposes a stable typed surface
	// (currently in flight per workflow#640).
	//
	// Until then, the plugin is built + tested in isolation.
}
