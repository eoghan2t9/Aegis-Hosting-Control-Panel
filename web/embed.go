// Package web embeds the Aegis frontend (HTML/CSS/JS) into the panel binary
// so the UI ships with the server and needs no separate deployment step.
package web

import "embed"

// FS is the embedded frontend filesystem.
//
//go:embed index.html css js
var FS embed.FS
