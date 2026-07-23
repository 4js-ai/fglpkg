#!/usr/bin/env python3
"""Local mock of the OSV.dev query API for `fglpkg audit`.

fglpkg is pointed here via FGLPKG_AUDIT_URL=http://127.0.0.1:PORT/v1/query.
It POSTs {"package":{"purl":"pkg:maven/<group>/<artifact>@<version>"}} once per
JAR and expects {"vulns":[...]}. To exercise both outcomes deterministically,
this mock returns a canned HIGH advisory when the requested PURL contains the
substring "vuln", and an empty result otherwise.

  python3 osv_server.py --port-file <file>
"""
import argparse, json, sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

def advisory():
    return {
        "id": "GHSA-mock-0000-0001",
        "summary": "Mock remote-code-execution advisory (functional test)",
        "details": "Synthetic advisory returned by the fglpkg functional-test OSV mock.",
        "aliases": ["CVE-2026-0001"],
        "severity": [{"type": "CVSS_V3",
                      "score": "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H"}],
        "references": [{"type": "ADVISORY",
                        "url": "https://example.test/advisories/GHSA-mock-0000-0001"}],
        "database_specific": {"severity": "HIGH"},
    }

class Handler(BaseHTTPRequestHandler):
    def log_message(self, fmt, *args):
        sys.stderr.write("%s %s\n" % (self.command, self.path))

    def do_POST(self):
        n = int(self.headers.get("Content-Length", 0) or 0)
        raw = self.rfile.read(n) if n else b"{}"
        try:
            purl = json.loads(raw).get("package", {}).get("purl", "")
        except Exception:
            purl = ""
        vulns = [advisory()] if "vuln" in purl.lower() else []
        body = json.dumps({"vulns": vulns}).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        self.send_response(404)
        self.send_header("Content-Length", "0")
        self.end_headers()

def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--port-file", required=True)
    a = ap.parse_args()
    httpd = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    port = httpd.server_address[1]
    with open(a.port_file, "w") as f:
        f.write(str(port))
    sys.stderr.write("mock osv listening on 127.0.0.1:%d\n" % port)
    httpd.serve_forever()

if __name__ == "__main__":
    main()
