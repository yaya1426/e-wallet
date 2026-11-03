// Package migrations embeds the golang-migrate SQL files (TDR-014) into the binary.
package migrations

import "embed"

// FS holds every NNNNNN_name.up.sql / .down.sql file in this directory.
//
//go:embed *.sql
var FS embed.FS
