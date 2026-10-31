package schemas

import "embed"

// Files embeds all Kates JSON Schemas (Draft 2020-12) for offline validation,
// IDE schema association, and the kates lsp language server.
//
//go:embed *.schema.json
var Files embed.FS
