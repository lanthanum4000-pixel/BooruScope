package web

import "embed"

//go:embed index.html app.js app.css i18n.js icon.svg drop.svg setup.html setup.js setup.css
var FS embed.FS
