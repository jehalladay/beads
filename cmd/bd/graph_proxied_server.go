package main

import (
	"context"
)

// runGraphCheckProxiedServer is the proxied-server path for `bd graph check`
// (beads-jc8k). The direct RunE dereferences the nil global `store`
// (DetectCycles) which panics on a hub-connected crew. This is a clean-mirror
// leg: DetectCycles exists on DependencyUseCase and runDepCyclesProxiedServer
// already calls it, mirroring dep_proxied_server.go. Output rendering is shared
// with the direct path via renderGraphCheck so the JSON/human shape is identical.
func runGraphCheckProxiedServer(ctx context.Context) error {
	uw := openDepProxiedUOW(ctx)
	defer uw.Close(ctx)

	cycles, err := uw.DependencyUseCase().DetectCycles(ctx)
	if err != nil {
		return HandleErrorRespectJSON("cycle detection failed: %v", err)
	}
	return renderGraphCheck(cycles)
}
