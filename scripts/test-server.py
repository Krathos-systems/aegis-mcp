"""Local web servers for the installer tests.

    python3 scripts/test-server.py files <dir> <port-file>   serve <dir> as a fake release
    python3 scripts/test-server.py aegis -     <port-file>   a fake Aegis: /api/admin/me
                                                             accepts the token "good"

The server binds 127.0.0.1 on a port the OS picks, so it cannot collide with
anything else on the machine, and writes that port to <port-file> once it is
listening. It skips the reverse DNS lookup that http.server does at start,
which can stall for a long time on macOS.
"""

import functools
import http.server
import os
import socketserver
import sys


class Server(http.server.ThreadingHTTPServer):
    daemon_threads = True

    def server_bind(self):
        socketserver.TCPServer.server_bind(self)  # not HTTPServer's: no getfqdn()
        self.server_name, self.server_port = "127.0.0.1", self.server_address[1]


class Files(http.server.SimpleHTTPRequestHandler):
    def log_message(self, *args):
        pass


class Aegis(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        ok = self.headers.get("Authorization") == "Bearer good"
        body = b'{"name":"ada","level":"editor"}' if ok else b'{"error":"token rejected"}'
        self.send_response(200 if ok else 401)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *args):
        pass


def main():
    mode, directory, port_file = sys.argv[1:4]
    handler = Aegis if mode == "aegis" else functools.partial(Files, directory=directory)
    server = Server(("127.0.0.1", 0), handler)
    with open(port_file + ".tmp", "w") as f:
        f.write(str(server.server_address[1]))
    os.replace(port_file + ".tmp", port_file)  # readers never see a half-written port
    server.serve_forever()


if __name__ == "__main__":
    main()
