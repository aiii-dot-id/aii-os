package bbb

import "time"

const MaxHTTPTimeout = 60 * time.Second

const DefaultInvokeTimeout = MaxHTTPTimeout + 30*time.Second
