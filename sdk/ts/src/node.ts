// Serving a queue, or several, with node:http: what takes a Request and
// gives a Response, as Fetch has them, on Node's own server.

import { createServer, type IncomingMessage, type Server, type ServerResponse } from "node:http";

export type Handler = (request: Request) => Promise<Response>;

/** The largest body a request may carry: a step's arguments. */
export const maxBody = 16 << 20;

/**
 * Serves the handlers given, by path, at a port of a host. A request that
 * cannot be read, or a handler that throws, is answered with an error:
 * the process goes on serving the others.
 */
export function listen(routes: Record<string, Handler>, port: number, host = "127.0.0.1"): Server {
  const server = createServer((req, res) => {
    serve(routes, req, res).catch((e: unknown) => {
      if (!res.headersSent) res.writeHead(e instanceof RequestError ? e.status : 500);
      res.end();
    });
  });
  server.listen(port, host);
  return server;
}

/** A request the server will not read. */
class RequestError extends Error {
  readonly status: number;

  constructor(status: number, why: string) {
    super(why);
    this.status = status;
  }
}

async function serve(routes: Record<string, Handler>, req: IncomingMessage, res: ServerResponse): Promise<void> {
  // The path alone says the handler, and is what is signed: the host a
  // request names, which anyone may write, is not read
  let url: URL;
  try {
    url = new URL(req.url ?? "/", "http://localhost");
  } catch {
    throw new RequestError(400, "the path cannot be read");
  }
  const handler = routes[url.pathname];
  if (handler === undefined) {
    res.writeHead(404).end();
    return;
  }
  const chunks: Buffer[] = [];
  let size = 0;
  for await (const chunk of req) {
    size += (chunk as Buffer).length;
    if (size > maxBody) throw new RequestError(413, "the body is too large");
    chunks.push(chunk as Buffer);
  }
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
