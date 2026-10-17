// Qoqi in VS Code: starts `qoqi lsp` and passes on what it says.
//
// The language server does the thinking; this file is the wire between it
// and the editor. It has no dependencies: the protocol is small enough to
// speak directly, messages of JSON each preceded by their length.

const vscode = require("vscode");
const { spawn } = require("child_process");
const fs = require("fs");
const path = require("path");

const LANGUAGE = "qoqi";

/** The running server, or null. */
let server = null;
let diagnostics = null;
let output = null;

// --- The server process ---

/** Whether a path is a file that is there. */
function isFile(file) {
  try {
    return fs.statSync(file).isFile();
  } catch (_) {
    return false;
  }
}

/**
 * Which program to run: the setting; else a qoqi program in the workspace
 * or in a folder above it, as when a folder inside the repository of qoqi
 * itself is open; else qoqi from the PATH.
 */
function programPath() {
  const configured = vscode.workspace.getConfiguration("qoqi").get("path");
  if (configured) return configured;
  const name = process.platform === "win32" ? "qoqi.exe" : "qoqi";
  for (const folder of vscode.workspace.workspaceFolders || []) {
    let dir = folder.uri.fsPath;
    for (;;) {
      const local = path.join(dir, name);
      if (isFile(local)) return local;
      const parent = path.dirname(dir);
      if (parent === dir) break;
      dir = parent;
    }
  }
  return "qoqi";
}

class Server {
  constructor(program) {
    this.nextId = 1;
    this.waiting = new Map(); // id -> { resolve, reject }
    this.buffer = Buffer.alloc(0);
    this.stopped = false;
    this.ready = false;
    this.opened = new Set(); // uris the server was told about

    const folders = vscode.workspace.workspaceFolders || [];
    this.process = spawn(program, ["lsp"], { cwd: folders.length ? folders[0].uri.fsPath : undefined });
    this.process.stdout.on("data", (chunk) => this.received(chunk));
    this.process.stderr.on("data", (chunk) => output.append(chunk.toString()));
    this.process.on("error", (error) => {
      this.fail(error);
      vscode.window.showErrorMessage(
        `Qoqi: cannot start the language server '${program}' (${error.message}). Set "qoqi.path" to the qoqi program.`
      );
    });
    this.process.on("exit", (code) => {
      if (!this.stopped) output.appendLine(`the language server ended with status ${code}`);
      this.fail(new Error("the language server has ended"));
    });
  }

  /** Gives up on every question still without an answer. */
  fail(error) {
    this.stopped = true;
    for (const { reject } of this.waiting.values()) reject(error);
    this.waiting.clear();
  }

  write(message) {
    if (this.stopped || !this.process.stdin.writable) return;
    const body = Buffer.from(JSON.stringify(message), "utf8");
    this.process.stdin.write(`Content-Length: ${body.length}\r\n\r\n`);
    this.process.stdin.write(body);
  }

  /** Asks something and waits for the answer. */
  ask(method, params) {
    if (this.stopped) return Promise.resolve(null);
    const id = this.nextId++;
    return new Promise((resolve, reject) => {
      this.waiting.set(id, { resolve, reject });
      this.write({ jsonrpc: "2.0", id, method, params });
    }).catch((error) => {
      output.appendLine(`${method}: ${error.message}`);
      return null;
    });
  }

  /** Says something that needs no answer. */
  tell(method, params) {
    this.write({ jsonrpc: "2.0", method, params });
  }

  /** Takes what the server wrote apart into messages. */
  received(chunk) {
    this.buffer = Buffer.concat([this.buffer, chunk]);
    for (;;) {
      const end = this.buffer.indexOf("\r\n\r\n");
      if (end < 0) return;
      const header = this.buffer.slice(0, end).toString("ascii");
      const match = /content-length:\s*(\d+)/i.exec(header);
      const length = match ? parseInt(match[1], 10) : 0;
      const start = end + 4;
      if (this.buffer.length < start + length) return;
      const body = this.buffer.slice(start, start + length).toString("utf8");
      this.buffer = this.buffer.slice(start + length);
      try {
        this.handle(JSON.parse(body));
      } catch (error) {
        output.appendLine(`a message could not be read: ${error.message}`);
      }
    }
  }

  handle(message) {
    if (message.id !== undefined && message.method === undefined) {
      const asked = this.waiting.get(message.id);
      if (!asked) return;
      this.waiting.delete(message.id);
      if (message.error) asked.reject(new Error(message.error.message));
      else asked.resolve(message.result);
      return;
    }
    if (message.method === "textDocument/publishDiagnostics") {
      const uri = vscode.Uri.parse(message.params.uri);
      diagnostics.set(uri, message.params.diagnostics.map(toDiagnostic));
    }
  }

  async start() {
    await this.ask("initialize", {
      processId: process.pid,
      rootUri: (vscode.workspace.workspaceFolders || []).map((f) => f.uri.toString())[0] || null,
      capabilities: {},
    });
    this.tell("initialized", {});
    this.ready = true;
    for (const document of vscode.workspace.textDocuments) this.open(document);
  }

  open(document) {
    if (!this.ready || document.languageId !== LANGUAGE || this.opened.has(document.uri.toString())) return;
    this.opened.add(document.uri.toString());
    this.tell("textDocument/didOpen", {
      textDocument: { uri: document.uri.toString(), languageId: LANGUAGE, version: document.version, text: document.getText() },
    });
  }

  change(document) {
    if (!this.opened.has(document.uri.toString())) return;
    this.tell("textDocument/didChange", {
      textDocument: { uri: document.uri.toString(), version: document.version },
      contentChanges: [{ text: document.getText() }],
    });
  }

  save(document) {
    if (!this.opened.has(document.uri.toString())) return;
    this.tell("textDocument/didSave", { textDocument: { uri: document.uri.toString() } });
  }

  close(document) {
    if (!this.opened.delete(document.uri.toString())) return;
    this.tell("textDocument/didClose", { textDocument: { uri: document.uri.toString() } });
  }

  async stop() {
    if (this.stopped) return;
    await Promise.race([this.ask("shutdown", null), new Promise((resolve) => setTimeout(resolve, 1000))]);
    this.tell("exit", null);
    this.stopped = true;
    setTimeout(() => this.process.kill(), 500);
  }
}

// --- From the protocol's values to the editor's ---

function toRange(range) {
  return new vscode.Range(range.start.line, range.start.character, range.end.line, range.end.character);
}

function toDiagnostic(d) {
  const severities = {
    1: vscode.DiagnosticSeverity.Error,
    2: vscode.DiagnosticSeverity.Warning,
    3: vscode.DiagnosticSeverity.Information,
    4: vscode.DiagnosticSeverity.Hint,
  };
  const diagnostic = new vscode.Diagnostic(toRange(d.range), d.message, severities[d.severity] ?? vscode.DiagnosticSeverity.Error);
  diagnostic.code = d.code;
  diagnostic.source = d.source || LANGUAGE;
  return diagnostic;
}

// The protocol numbers kinds from 1, the editor from 0, in the same order.
function toSymbol(s) {
  const symbol = new vscode.DocumentSymbol(s.name, s.detail || "", s.kind - 1, toRange(s.range), toRange(s.selectionRange));
  symbol.children = (s.children || []).map(toSymbol);
  return symbol;
}

function place(document, position) {
  return { textDocument: { uri: document.uri.toString() }, position: { line: position.line, character: position.character } };
}

// --- Commands that run qoqi ---

/** The terminal the commands run in, kept from one run to the next. */
let terminal = null;

/** Puts an argument the way a shell reads it as one. */
function quoted(text) {
  return /^[\w@%+=:,./-]+$/.test(text) ? text : `'${text.replace(/'/g, `'\\''`)}'`;
}

/** Runs qoqi with arguments in a terminal, where what it prints is read. */
function runInTerminal(args) {
  if (!terminal || terminal.exitStatus !== undefined) {
    terminal = vscode.window.createTerminal("Qoqi");
  }
  terminal.show(true);
  terminal.sendText([programPath(), ...args].map(quoted).join(" "));
}

// --- Starting and stopping ---

async function startServer() {
  if (!vscode.workspace.getConfiguration("qoqi").get("server.enabled")) return;
  server = new Server(programPath());
  await server.start();
}

async function stopServer() {
  const old = server;
  server = null;
  diagnostics.clear();
  if (old) await old.stop();
}

async function activate(context) {
  output = vscode.window.createOutputChannel("Qoqi");
  diagnostics = vscode.languages.createDiagnosticCollection(LANGUAGE);
  context.subscriptions.push(output, diagnostics);

  context.subscriptions.push(
    vscode.workspace.onDidOpenTextDocument((document) => server && server.open(document)),
    vscode.workspace.onDidChangeTextDocument((event) => server && server.change(event.document)),
    vscode.workspace.onDidSaveTextDocument((document) => server && server.save(document)),
    vscode.workspace.onDidCloseTextDocument((document) => {
      if (server) server.close(document);
      diagnostics.delete(document.uri);
    }),
    vscode.workspace.onDidChangeConfiguration(async (event) => {
      if (event.affectsConfiguration("qoqi")) {
        await stopServer();
        await startServer();
      }
    }),
    vscode.commands.registerCommand("qoqi.restartServer", async () => {
      await stopServer();
      await startServer();
    })
  );

  context.subscriptions.push(
    vscode.languages.registerHoverProvider(LANGUAGE, {
      async provideHover(document, position) {
        const found = server && (await server.ask("textDocument/hover", place(document, position)));
        if (!found) return null;
        return new vscode.Hover(new vscode.MarkdownString(found.contents.value), found.range && toRange(found.range));
      },
    }),
    vscode.languages.registerDefinitionProvider(LANGUAGE, {
      async provideDefinition(document, position) {
        const found = server && (await server.ask("textDocument/definition", place(document, position)));
        if (!found) return null;
        return new vscode.Location(vscode.Uri.parse(found.uri), toRange(found.range));
      },
    }),
    vscode.languages.registerDocumentSymbolProvider(LANGUAGE, {
      async provideDocumentSymbols(document) {
        const found = server && (await server.ask("textDocument/documentSymbol", { textDocument: { uri: document.uri.toString() } }));
        return (found || []).map(toSymbol);
      },
    }),
    vscode.languages.registerCompletionItemProvider(
      LANGUAGE,
      {
        async provideCompletionItems(document, position) {
          if (!server) return [];
          // The server is asked about the text as it is now, letter just typed included
          server.change(document);
          const found = await server.ask("textDocument/completion", place(document, position));
          return (found || []).map((s) => {
            const item = new vscode.CompletionItem(s.label, s.kind - 1);
            item.detail = s.detail;
            return item;
          });
        },
      },
      "."
    ),
    // Format Document, and formatting on save for who has turned it on
    vscode.languages.registerDocumentFormattingEditProvider(LANGUAGE, {
      async provideDocumentFormattingEdits(document) {
        if (!server) return [];
        // The server formats the text it has: the one in the editor now
        server.change(document);
        const edits = await server.ask("textDocument/formatting", {
          textDocument: { uri: document.uri.toString() },
          options: { tabSize: 4, insertSpaces: true },
        });
        // The server names the whole file by a line past its last one
        const whole = new vscode.Range(0, 0, document.lineCount, 0);
        return (edits || []).map((edit) => vscode.TextEdit.replace(document.validateRange(whole), edit.newText));
      },
    })
  );

  context.subscriptions.push(
    vscode.commands.registerCommand("qoqi.runTests", () => runInTerminal(["test"])),
    vscode.commands.registerCommand("qoqi.runFileTests", () => {
      const editor = vscode.window.activeTextEditor;
      if (!editor || editor.document.languageId !== LANGUAGE) {
        vscode.window.showInformationMessage("Qoqi: open a test file (name_test.qi) to run its tests.");
        return;
      }
      editor.document.save().then(() => runInTerminal(["test", editor.document.uri.fsPath]));
    })
  );

  await startServer();
}

async function deactivate() {
  if (server) await server.stop();
}

module.exports = { activate, deactivate };
