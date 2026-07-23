#!/usr/bin/env python3
"""Local mock of the fglpkg registry (see ../MOCK-PROTOCOL.md).

Serves the fglpkg /registry/* protocol from a fixtures directory so the
network-dependent commands (install/search/info/outdated/publish/deprecate) run
hermetically. fglpkg is pointed here via FGLPKG_REGISTRY=http://127.0.0.1:PORT.

  python3 registry_server.py --fixtures <dir> --port-file <file>

<dir>/packages.json describes packages; artifact zips live alongside it. The
server computes size_bytes + sha256 from the real zip bytes so checksums always
match what it serves.
"""
import argparse, hashlib, json, os, re, sys, urllib.parse
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

def canonical(slug):
    return re.sub(r'[-_.]+', '-', slug).lower()

class Fixtures:
    def __init__(self, d):
        self.dir = d
        spec = json.load(open(os.path.join(d, "packages.json")))
        self.packages = {}          # canonical slug -> package dict
        self.downloads = {}         # download_url path -> zip file path
        for p in spec.get("packages", []):
            slug = canonical(p["slug"])
            for v in p.get("versions", []):
                for a in v.get("artifacts", []):
                    zpath = os.path.join(d, a["zip"])
                    raw = open(zpath, "rb").read()
                    a["_size"] = len(raw)
                    a["_sha256"] = hashlib.sha256(raw).hexdigest()
                    a["_download_url"] = "/registry/packages/%s/versions/%s/artifacts/%s" % (
                        slug, v["version"], a["variant"])
                    self.downloads[a["_download_url"]] = zpath
            self.packages[slug] = p

    def detail(self, slug):
        p = self.packages.get(canonical(slug))
        if not p:
            return None
        versions = []
        for v in p.get("versions", []):
            arts = [{
                "variant": a["variant"],
                "filename": os.path.basename(a["zip"]),
                "size_bytes": a["_size"],
                "sha256": a["_sha256"],
                "download_url": a["_download_url"],
                "uploaded_at": "2026-01-02T00:00:00Z",
                "uploader": p.get("owner", {}).get("name", "mock"),
                "signature": None,
            } for a in v.get("artifacts", [])]
            versions.append({
                "version": v["version"], "status": "published", "changelog": "",
                "tags": {}, "submitted_at": "2026-01-01T00:00:00Z",
                "published_at": "2026-01-02T00:00:00Z", "review_comment": "",
                "repository": v.get("repository", ""), "author": v.get("author", ""),
                "license": v.get("license", ""), "genero": v.get("genero", ""),
                "dependencies": {"fgl": {}, "java": []}, "readme": "", "userguide": "",
                "deprecated": False, "deprecation_message": "", "moved_to": "",
                "artifacts": arts,
            })
        latest = p["versions"][-1]["version"] if p.get("versions") else ""
        return {
            "slug": canonical(p["slug"]), "name": p.get("name", p["slug"]),
            "description": p.get("description", ""), "visibility": "public",
            "owner": p.get("owner", {"partner_id": "mock", "name": "mock"}),
            "status": "published", "latest_version": latest, "downloads": 0,
            "tags": {}, "deprecated": False, "deprecation_message": "", "moved_to": "",
            "genero": p.get("genero", ""), "versions": versions,
        }

    def listed(self, p):
        return {
            "slug": canonical(p["slug"]), "name": p.get("name", p["slug"]),
            "description": p.get("description", ""), "visibility": "public",
            "owner": p.get("owner", {"partner_id": "mock", "name": "mock"}),
            "status": "published",
            "latest_version": p["versions"][-1]["version"] if p.get("versions") else "",
            "downloads": 0, "tags": {}, "deprecated": False,
            "deprecation_message": "", "moved_to": "", "genero": p.get("genero", ""),
        }

    def search(self, term):
        term = (term or "").lower()
        hits = [self.listed(p) for p in self.packages.values()
                if term in canonical(p["slug"]) or term in p.get("name", "").lower()
                or term in p.get("description", "").lower()]
        return {"packages": hits, "page": 1, "pageSize": 20, "total": len(hits)}

FX = None

class Handler(BaseHTTPRequestHandler):
    def log_message(self, fmt, *args):
        sys.stderr.write("%s %s\n" % (self.command, self.path))

    def _json(self, code, obj):
        body = json.dumps(obj).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def _empty(self, code):
        self.send_response(code)
        self.send_header("Content-Length", "0")
        self.end_headers()

    def _drain(self):
        n = int(self.headers.get("Content-Length", 0) or 0)
        if n:
            self.rfile.read(n)

    def do_GET(self):
        parsed = urllib.parse.urlparse(self.path)
        path = parsed.path
        # 1) artifact download (exact match on a known download_url)
        if path in FX.downloads:
            raw = open(FX.downloads[path], "rb").read()
            self.send_response(200)
            self.send_header("Content-Type", "application/zip")
            self.send_header("Content-Length", str(len(raw)))
            self.end_headers()
            self.wfile.write(raw)
            return
        # 2) search
        if path == "/registry/packages":
            q = urllib.parse.parse_qs(parsed.query).get("q", [""])[0]
            self._json(200, FX.search(q))
            return
        # 3) package detail
        m = re.fullmatch(r"/registry/packages/([^/]+)", path)
        if m:
            slug = urllib.parse.unquote(m.group(1))
            d = FX.detail(slug)
            if d is None:
                self._empty(404)      # ErrNotFound / clean publish precheck
            else:
                self._json(200, d)
            return
        # 4) self-update latest — optional, 404 is a silent no-op
        if path == "/registry/fglpkg/latest":
            self._empty(404)
            return
        self._empty(404)

    def do_POST(self):
        self._drain()
        path = urllib.parse.urlparse(self.path).path
        if path == "/registry/packages":
            self._json(201, {"slug": "created"}); return
        if re.fullmatch(r"/registry/packages/[^/]+/versions", path):
            self._json(201, {"version": "created"}); return
        if re.fullmatch(r"/registry/packages/[^/]+/versions/[^/]+/submit", path):
            self._json(200, {"status": "pending"}); return
        self._empty(404)

    def do_PUT(self):
        self._drain()
        path = urllib.parse.urlparse(self.path).path
        if re.fullmatch(r"/registry/packages/[^/]+/versions/[^/]+/artifacts/[^/]+", path):
            self._json(200, {"uploaded": True}); return
        self._empty(404)

    def do_PATCH(self):
        self._drain()
        path = urllib.parse.urlparse(self.path).path
        if re.fullmatch(r"/registry/packages/[^/]+(/versions/[^/]+)?", path):
            self._empty(200); return    # deprecate D1/D2 + publish step 5
        self._empty(404)

def main():
    global FX
    ap = argparse.ArgumentParser()
    ap.add_argument("--fixtures", required=True)
    ap.add_argument("--port-file", required=True)
    a = ap.parse_args()
    FX = Fixtures(a.fixtures)
    httpd = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    port = httpd.server_address[1]
    with open(a.port_file, "w") as f:      # signal readiness AFTER bind
        f.write(str(port))
    sys.stderr.write("mock registry listening on 127.0.0.1:%d\n" % port)
    httpd.serve_forever()

if __name__ == "__main__":
    main()
