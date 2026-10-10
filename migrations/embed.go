// Package migrations embeds the product's SQL migrations.
// The runner consumes FS with subdir "." (framework pgmigrate contract).
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
