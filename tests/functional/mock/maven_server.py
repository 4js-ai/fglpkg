#!/usr/bin/env python3
"""Local mock of a Maven repository (Maven Central or a JFrog Artifactory Maven
repo) for the fglpkg functional suite.

fglpkg fetches JAR dependencies from a Maven2-layout base URL
(<base>/<group/with/slashes>/<artifactId>/<version>/<artifactId>-<version>.jar).
This server serves a fixed JAR body for any GET whose path ends in ``.jar`` so a
mirror can be pointed at it via FGLPKG_MAVEN_URL / the mavenMirror config, and
the download stays hermetic and offline.

  python3 maven_server.py --port-file <file> [--require-token <tok>]

--require-token makes the server answer 403 (the status Artifactory returns for a
bad/missing credential, which downloadAndVerify surfaces as an auth failure)
unless the request carries ``Authorization: Bearer <tok>``. Every request path is
logged to stderr so a test can confirm the mirror — not repo1.maven.org — was hit.
"""
import argparse, sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

JAR_BODY = b"PK\x03\x04mock-jar-bytes-for-fglpkg-functional-tests"
REQUIRE_TOKEN = None

class Handler(BaseHTTPRequestHandler):
    def log_message(self, fmt, *args):
        sys.stderr.write("%s %s\n" % (self.command, self.path))

    def do_GET(self):
        if REQUIRE_TOKEN is not None:
            auth = self.headers.get("Authorization", "")
            if auth != "Bearer %s" % REQUIRE_TOKEN:
                self.send_response(403)          # Artifactory uses 403, not 401
                self.send_header("Content-Length", "0")
                self.end_headers()
                return
        if self.path.endswith(".jar"):
            self.send_response(200)
            self.send_header("Content-Type", "application/java-archive")
            self.send_header("Content-Length", str(len(JAR_BODY)))
            self.end_headers()
            self.wfile.write(JAR_BODY)
            return
        self.send_response(404)
        self.send_header("Content-Length", "0")
        self.end_headers()

def main():
    global REQUIRE_TOKEN
    ap = argparse.ArgumentParser()
    ap.add_argument("--port-file", required=True)
    ap.add_argument("--require-token", default=None)
    a = ap.parse_args()
    REQUIRE_TOKEN = a.require_token
    httpd = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    port = httpd.server_address[1]
    with open(a.port_file, "w") as f:            # signal readiness AFTER bind
        f.write(str(port))
    sys.stderr.write("mock maven listening on 127.0.0.1:%d\n" % port)
    httpd.serve_forever()

if __name__ == "__main__":
    main()
