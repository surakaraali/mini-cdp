// Package api holds the OpenAPI description of the HTTP API.
package api

import _ "embed"

// Spec is api/openapi.yaml, compiled into the binary and served at GET /openapi.yaml.
//
//go:embed openapi.yaml
var Spec []byte
