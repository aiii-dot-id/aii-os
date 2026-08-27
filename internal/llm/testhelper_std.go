package llm

import "os"

// osStderr lets tests swap the standard logger's output and restore it
// without importing log's default writer twice.
var osStderr = os.Stderr
