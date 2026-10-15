package web

import _ "embed"

// IndexHTML is the reader served by the installed CLI, independent of cwd.
//
//go:embed index.html
var IndexHTML []byte
