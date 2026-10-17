// Serving a queue, or several, with node:http: what takes a Request and
// gives a Response, as Fetch has them, on Node's own server.

import { createServer, type IncomingMessage, type Server, type ServerResponse } from "node:http";

export type Handler = (request: Request) => Promise<Response>;

/** Serves the handlers given, by path, at a port of a host. */
export function listen(routes: Record<string, Handler>, port: number, host = "127.0.0.1"): Server {
  const server = createServer((req, res) => {
    void serve(routes, req, res);
  });
  server.listen(port, host);
  return server;
}

async function serve(routes: Record<string, Handler>, req: IncomingMessage, res: ServerResponse): Promise<void> {
  const url = new URL(req.url ?? "/", `http://${req.headers.host ?? "localhost"}`);
  const handler = routes[url.pathname];
  if (handler === undefined) {
    res.writeHead(404).end();
    return;
  }
  const chunks: Buffer[] = [];
  for await (const chunk of req) chunks.push(chunk as Buffer);
  const headers = new Headers();
  for (const [name, value] of Object.entries(req.headers)) {
    if (typeof value === "string") headers.set(name, value);
  }
  const method = req.method ?? "GET";
  const request = new Request(url, { method, headers, body: method === "GET" || method === "HEAD" ? null : Buffer.concat(chunks) });
  const response = await handler(request);
  res.writeHead(response.status, Object.fromEntries(response.headers));
  res.end(Buffer.from(await response.arrayBuffer()));
}
