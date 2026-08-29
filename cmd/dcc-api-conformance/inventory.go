package main

import (
	"fmt"
	"io"
)

type endpointDefinition struct {
	Method   string
	Path     string
	Coverage string
}

// publicEndpointInventory is the reviewable inventory of the server's public
// HTTP and WebSocket surface. Coverage identifies the conformance scenario
// responsible for the endpoint; WebSocket behavior is exercised by the API
// integration suite because the CLI intentionally remains HTTP-only.
var publicEndpointInventory = []endpointDefinition{
	{"GET", "/healthz", "passive"},
	{"GET", "/api/v1/system/info", "passive"},
	{"POST", "/api/v1/auth/login", "passive"},
	{"POST", "/api/v1/auth/refresh", "passive"},
	{"POST", "/api/v1/auth/logout", "passive"},
	{"GET", "/api/v1/me", "passive"},
	{"GET", "/api/v1/track-power", "passive"},
	{"GET", "/api/v1/station/status", "passive"},
	{"PUT", "/api/v1/track-power", "active"},
	{"POST", "/api/v1/emergency-stop", "active"},
	{"GET", "/api/v1/locomotives", "passive"},
	{"POST", "/api/v1/locomotives", "configuration"},
	{"GET", "/api/v1/locomotives/{}", "passive"},
	{"PUT", "/api/v1/locomotives/{}", "configuration"},
	{"DELETE", "/api/v1/locomotives/{}", "configuration"},
	{"POST", "/api/v1/locomotives/{}/control-lease", "active"},
	{"PUT", "/api/v1/control-leases/{}/heartbeat", "active"},
	{"DELETE", "/api/v1/control-leases/{}", "active"},
	{"PUT", "/api/v1/locomotives/{}/throttle", "active"},
	{"PUT", "/api/v1/locomotives/{}/functions/{}", "active"},
	{"GET", "/api/v1/blocks", "passive"},
	{"GET", "/api/v1/turnouts", "passive"},
	{"PUT", "/api/v1/turnouts/{}", "active"},
	{"GET", "/api/v1/routes", "passive"},
	{"POST", "/api/v1/routes/{}/reserve", "active"},
	{"POST", "/api/v1/routes/{}/activate", "active"},
	{"POST", "/api/v1/routes/{}/release", "active"},
	{"GET", "/api/v1/exports/rolling-stock", "passive"},
	{"POST", "/api/v1/imports/rolling-stock", "configuration"},
	{"GET", "/api/v1/layout/export", "passive"},
	{"POST", "/api/v1/layout/import", "configuration"},
	{"GET", "/api/v1/events", "websocket"},
}

func writeEndpointInventory(w io.Writer) {
	for _, endpoint := range publicEndpointInventory {
		fmt.Fprintf(w, "%-6s %-52s %s\n", endpoint.Method, endpoint.Path, endpoint.Coverage)
	}
}
