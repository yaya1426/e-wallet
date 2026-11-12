// This file only keeps the Go tool out of web/ (node_modules may contain Go sources, which
// `go test ./...` from the repo root would otherwise pick up). The web app is not Go code.
module github.com/yaya1426/e-wallet/web

go 1.27
