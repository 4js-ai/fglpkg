#!/usr/bin/env python3
"""Local mock of a JFrog Artifactory generic repository for the fglpkg
functional suite.

fglpkg reads an Artifactory repository through two APIs: the storage API for
listings and checksums (``/api/storage/<repo>/<path>``, JSON) and plain content
GETs for the bytes (``/<repo>/<path>``). This serves both over a package tree
given on the command line.

  python3 artifactory_server.py --port-file <file> [--repo <key>] \
      --pkg [<repo>/]<folder>:<version>[:<variant>] [--pkg ...] \
      [--refuse <repo>[/<folder>]]

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

SEVERAL REPOSITORIES, one process. A --pkg spec may name the repository that
holds it, and every repository named anywhere is served. That is what lets a
test put the same folder in two repositories (the collision guard) or stand a
repository that answers beside one that holds the package.

REFUSING A PATH. ``--refuse <repo>`` answers 401 to everything in that
repository, and ``--refuse <repo>/<folder>`` answers 401 only under that
folder, which is a repository whose credentials do not cover one package. The
distinction matters: fglpkg must never read a 401 as "the package is not here",
and the narrow form is the shape that tells the two apart, because the
canonical and the legacy spelling of one name can then get different answers
from the same repository.
"""
import argparse, hashlib, json, sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

DEFAULT_REPO = "generic-local"
TREES = {}         # repo -> folder -> version -> {"zip": filename, "body": bytes, "manifest": bytes}
REFUSE = set()     # (repo, folder) pairs; folder None means the whole repository
BASE = ""          # http://127.0.0.1:<port>, for downloadUri


def split_repo(spec, default):
    """Split a leading '<repo>/' off a spec. Folder names never hold a slash."""
    repo, sep, rest = spec.partition("/")
    return (repo, rest) if sep else (default, spec)


def add_package(spec, default_repo):
    repo, rest = split_repo(spec, default_repo)
    parts = rest.split(":")
    folder, version = parts[0], parts[1]
    variant = parts[2] if len(parts) > 2 else "genero6"
    body = ("PK\x03\x04mock-fglpkg-%s-%s" % (folder, version)).encode()
    manifest = json.dumps({
        "name": folder, "version": version,
        "description": "mock package %s" % folder,
        "genero": ">=3.20", "license": "MIT", "author": "fglpkg tests",
    }, indent=2).encode()
    TREES.setdefault(repo, {}).setdefault(folder, {})[version] = {
        "zip": "%s-%s-%s.zip" % (folder, version, variant),
        "body": body,
        "manifest": manifest,
    }


def add_refusal(spec):
    """<repo> refuses everything; <repo>/<folder> refuses only that folder."""
    repo, sep, folder = spec.partition("/")
    REFUSE.add((repo, folder if sep else None))
    TREES.setdefault(repo, {})                  # a known, if unreadable, repository


def refuses(repo, folder):
    return (repo, None) in REFUSE or (folder is not None and (repo, folder) in REFUSE)


class Handler(BaseHTTPRequestHandler):
    def log_message(self, fmt, *args):
        sys.stderr.write("%s %s\n" % (self.command, self.path))

    def _json(self, obj, status=200):
        data = json.dumps(obj).encode()
        self.send_response(status)
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

    def _401(self):
        # Artifactory's own shape. fglpkg reads the status, not the body, but a
        # mock that answered an empty 401 would not show that in the log.
        self._json({"errors": [{"status": 401, "message": "Bad credentials"}]}, 401)

    @staticmethod
    def _children(names, folder):
        return {"children": [{"uri": "/" + n, "folder": folder} for n in names]}

    def do_GET(self):
        p = [s for s in self.path.split("?")[0].split("/") if s]
        storage = len(p) >= 3 and p[0] == "api" and p[1] == "storage"
        repo, rest = (p[2], p[3:]) if storage else (p[0] if p else "", p[1:])
        tree = TREES.get(repo)
        if tree is None:
            return self._404()
        if refuses(repo, rest[0] if rest else None):
            return self._401()
        # ---- storage API: listings and checksums ----
        if storage:
            if not rest:                                     # the repository root
                return self._json(self._children(sorted(tree), True))
            versions = tree.get(rest[0])
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
                    "downloadUri": "%s/%s/%s/%s/%s" % (BASE, repo, rest[0], rest[1], ver["zip"]),
                    "checksums": {"sha256": hashlib.sha256(ver["body"]).hexdigest()},
                })
            return self._404()
        # ---- content: the sidecar manifest and the artifact itself ----
        if len(rest) == 3:
            ver = tree.get(rest[0], {}).get(rest[1])
            if ver is None:
                return self._404()
            if rest[2] == "fglpkg.json":
                return self._bytes(ver["manifest"], "application/json")
            if rest[2] == ver["zip"]:
                return self._bytes(ver["body"], "application/zip")
        return self._404()


def main():
    global BASE
    ap = argparse.ArgumentParser()
    ap.add_argument("--port-file", required=True)
    ap.add_argument("--repo", default=DEFAULT_REPO,
                    help="repository key for specs that do not name one")
    ap.add_argument("--pkg", action="append", default=[],
                    help="[<repo>/]<folder>:<version>[:<variant>], repeatable")
    ap.add_argument("--refuse", action="append", default=[],
                    help="<repo>[/<folder>] answers 401, repeatable")
    a = ap.parse_args()
    TREES.setdefault(a.repo, {})
    for spec in a.pkg:
        add_package(spec, a.repo)
    for spec in a.refuse:
        add_refusal(spec)
    httpd = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    port = httpd.server_address[1]
    BASE = "http://127.0.0.1:%d" % port
    with open(a.port_file, "w") as f:            # signal readiness AFTER bind
        f.write(str(port))
    sys.stderr.write("mock artifactory listening on 127.0.0.1:%d repos=%s refusing=%s\n"
                     % (port,
                        ";".join("%s:%s" % (r, ",".join(sorted(TREES[r])) or "(none)")
                                 for r in sorted(TREES)),
                        ",".join("%s/%s" % (r, f or "*") for r, f in sorted(REFUSE, key=str)) or "(nothing)"))
    httpd.serve_forever()


if __name__ == "__main__":
    main()
