#!/usr/bin/env python3
"""Minimal fake Traefik API for the demo: core-v2 only needs GET /api/rawdata
to observe the runtime. It reports an empty, healthy runtime."""
import json, sys
from http.server import BaseHTTPRequestHandler, HTTPServer

class H(BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path.startswith('/api/rawdata'):
            body = json.dumps({"routers": {}, "services": {}, "middlewares": {}}).encode()
            self.send_response(200); self.send_header('Content-Type', 'application/json')
        else:
            body = b'{}'; self.send_response(404)
        self.send_header('Content-Length', str(len(body))); self.end_headers(); self.wfile.write(body)
    def log_message(self, *a): pass

HTTPServer(('127.0.0.1', int(sys.argv[1]) if len(sys.argv) > 1 else 8080), H).serve_forever()
