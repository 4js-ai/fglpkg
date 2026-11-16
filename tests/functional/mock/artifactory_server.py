#!/usr/bin/env python3
"""Local mock of a JFrog Artifactory generic repository for the fglpkg
functional suite.

fglpkg reads an Artifactory repository through two APIs: the storage API for
listings and checksums (``/api/storage/<repo>/<path>``, JSON) and plain content
GETs for the bytes (``/<repo>/<path>``). This serves both over a package tree
given on the command line.

  python3 artifactory_server.py --port-file <file> [--repo <key>] \
      --pkg <folder>:<version>[:<variant>] [--pkg ...]

The tree is held in memory and matched EXACTLY, so folder names are
case-sensitive however the host filesystem behaves — which is the whole point
for GIS-610, where ``DateTime/`` and ``datetime/`` are two different packages
and a macOS filesystem would quietly merge them.

Each package folder is laid out the way the publisher that wrote it did:

  <folder>/<version>/<folder>-<version>-<variant>.zip     the artifact
  <folder>/<version>/fglpkg.json                          the sidecar manifest

fglpkg <= 4.2.9 published under the manifest's name as typed, 4.2.10 publishes
under the canonical slug, and that difference is exactly what a --pkg folder
name expresses. Every request path is logged to stderr, so a test can assert
which spelling was asked for.
"""
import argparse, hashlib, json, sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

REPO = "generic-local"
TREE = {}          # folder -> version -> {"zip": filename, "body": bytes, "manifest": bytes}
BASE = ""          # http://127.0.0.1:<port>, for downloadUri


def add_package(spec):
    parts = spec.split(":")
    folder, version = parts[0], parts[1]
    variant = parts[2] if len(parts) > 2 else "genero6"
    body = ("PK\x03\x04mock-fglpkg-%s-%s" % (folder, version)).encode()
    manifest = json.dumps({
        "name": folder, "version": version,
        "description": "mock package %s" % folder,
        "genero": ">=3.20", "license": "MIT", "author": "fglpkg tests",
    }, indent=2).encode()
    TREE.setdefault(folder, {})[version] = {
        "zip": "%s-%s-%s.zip" % (folder, version, variant),
        "body": body,
        "manifest": manifest,
    }


class Handler(BaseHTTPRequestHandler):
    def log_message(self, fmt, *args):
        sys.stderr.write("%s %s\n" % (self.command, self.path))

    def _json(self, obj):
        data = json.dumps(obj).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def _bytes(self, data, ctype="application/octet-stream"):
        self.send_response(200)
        self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def _404(self):
        self.send_response(404)
        self.send_header("Content-Length", "0")
        self.end_headers()

    @staticmethod
    def _children(names, folder):
        return {"children": [{"uri": "/" + n, "folder": folder} for n in names]}

    def do_GET(self):
        p = [s for s in self.path.split("?")[0].split("/") if s]
        # ---- storage API: listings and checksums ----
        if len(p) >= 3 and p[0] == "api" and p[1] == "storage" and p[2] == REPO:
            rest = p[3:]
            if not rest:                                     # the repository root
                return self._json(self._children(sorted(TREE), True))
            versions = TREE.get(rest[0])
            if versions is None:
                return self._404()
            if len(rest) == 1:                               # a package folder
                return self._json(self._children(sorted(versions), True))
            ver = versions.get(rest[1])
            if ver is None:
                return self._404()
            if len(rest) == 2:                               # a version folder
                return self._json(self._children([ver["zip"], "fglpkg.json"], False))
            if len(rest) == 3 and rest[2] == ver["zip"]:     # the artifact's file info
                return self._json({
                    "downloadUri": "%s/%s/%s/%s/%s" % (BASE, REPO, rest[0], rest[1], ver["zip"]),
                    "checksums": {"sha256": hashlib.sha256(ver["body"]).hexdigest()},
                })
            return self._404()
        # ---- content: the sidecar manifest and the artifact itself ----
        if len(p) == 4 and p[0] == REPO:
            ver = TREE.get(p[1], {}).get(p[2])
            if ver is None:
                return self._404()
            if p[3] == "fglpkg.json":
                return self._bytes(ver["manifest"], "application/json")
            if p[3] == ver["zip"]:
                return self._bytes(ver["body"], "application/zip")
        return self._404()


def main():
    global REPO, BASE
    ap = argparse.ArgumentParser()
    ap.add_argument("--port-file", required=True)
    ap.add_argument("--repo", default="generic-local")
    ap.add_argument("--pkg", action="append", default=[],
                    help="<folder>:<version>[:<variant>], repeatable")
    a = ap.parse_args()
    REPO = a.repo
    for spec in a.pkg:
        add_package(spec)
    httpd = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    port = httpd.server_address[1]
    BASE = "http://127.0.0.1:%d" % port
    with open(a.port_file, "w") as f:            # signal readiness AFTER bind
        f.write(str(port))
    sys.stderr.write("mock artifactory listening on 127.0.0.1:%d repo=%s packages=%s\n"
                     % (port, REPO, ",".join(sorted(TREE)) or "(none)"))
    httpd.serve_forever()


if __name__ == "__main__":
    main()
