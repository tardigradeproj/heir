#!/usr/bin/env python3
"""
Mock kubelet — minimal mTLS HTTPS server that stands in for a real kubelet
when testing the plane tunnel egress selector locally.

Usage:
    python3 docs/mock-kubelet.py \
        --cert   pki/nodes/worker1/kubelet-server.crt \
        --key    pki/nodes/worker1/kubelet-server.key \
        --ca-cert pki/ca.crt
"""

import ssl
import argparse
from http.server import HTTPServer, BaseHTTPRequestHandler


class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        cn = ""
        if self.connection.getpeercert():
            for field in self.connection.getpeercert().get("subject", []):
                for k, v in field:
                    if k == "commonName":
                        cn = v
        body = f"mock kubelet: ok (client={cn})\n".encode()
        self.send_response(200)
        self.send_header("Content-Type", "text/plain")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, fmt, *args):
        print(f"[mock-kubelet] {self.client_address[0]} {fmt % args}")


def main():
    p = argparse.ArgumentParser(description="Mock kubelet with mTLS")
    p.add_argument("--addr",    default="127.0.0.1")
    p.add_argument("--port",    default=10250, type=int)
    p.add_argument("--cert",    required=True, help="server certificate")
    p.add_argument("--key",     required=True, help="server key")
    p.add_argument("--ca-cert", required=True, dest="ca", help="CA cert for client verification")
    args = p.parse_args()

    ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    ctx.load_cert_chain(args.cert, args.key)
    ctx.load_verify_locations(args.ca)
    ctx.verify_mode = ssl.CERT_REQUIRED

    srv = HTTPServer((args.addr, args.port), Handler)
    srv.socket = ctx.wrap_socket(srv.socket, server_side=True)

    print(f"[mock-kubelet] listening on {args.addr}:{args.port} (mTLS)")
    srv.serve_forever()


if __name__ == "__main__":
    main()
