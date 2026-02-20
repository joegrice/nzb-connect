// Package web embeds the built frontend assets (web/dist) into the binary.
// Build the frontend first: cd web && npm run build
package web

import "embed"

//go:embed dist
var Dist embed.FS
