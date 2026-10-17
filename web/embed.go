package web

import "embed"

//go:embed index.html app.js app.css i18n.js icon.svg
var FS embed.FS
