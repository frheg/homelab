"""The page the hackathon pod shows until $APP_DIR/run.sh exists."""

import html
import http.server
import os

APP_DIR = html.escape(os.environ.get("APP_DIR", "/srv/hackathon/app"))
PAGE = f"""<!doctype html><meta charset="utf-8"><title>Hackathon</title>
<body style="font:16px system-ui;max-width:40em;margin:3em auto;padding:0 1em">
<h1>Nothing to run yet</h1>
<p>Put an executable <code>run.sh</code> in <code>{APP_DIR}</code> on kratos.
It must serve on port <code>$PORT</code> (8000), and requests keep the path prefix <code>$ROOT_PATH</code> (/hackathon).
The pod starts it within 10 s, and again whenever it stops.</p>
</body>""".encode()


class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200)
        self.send_header("Content-Type", "text/html; charset=utf-8")
        self.end_headers()
        self.wfile.write(PAGE)

    def log_message(self, format, *args):
        pass


http.server.HTTPServer.allow_reuse_address = True
http.server.HTTPServer(("0.0.0.0", int(os.environ.get("PORT", "8000"))), Handler).serve_forever()
