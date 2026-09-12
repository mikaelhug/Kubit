// Package web embeds the built SPA. Run `npm run build` in this directory first;
// a missing dist/ only yields the .gitkeep placeholder and an empty UI.
package web

import "embed"

//go:embed all:dist
var Dist embed.FS
