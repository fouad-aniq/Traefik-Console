package main

import "net/http"

// API-only development builds leave the static routes unregistered.
var webHandler http.Handler
