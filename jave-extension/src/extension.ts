import * as vscode from 'vscode';
import WebSocket from 'ws';
import * as path from 'path';
import * as fs from 'fs';

// ── Message protocol (must match jave-bridge/protocol.go) ────────────────────

type MessageKind =
  | 'write_file'    // full file write (creates or overwrites)
  | 'patch_file'    // replace lines start..end with new content
  | 'delete_file'   // delete a file
  | 'make_dir'      // create directory
  | 'shell_start'   // sandbox command started
  | 'shell_done'    // sandbox command finished (exit_code field)
  | 'shell_output'  // line of stdout/stderr from sandbox
  | 'ping';         // keepalive

interface JaveMessage {
  kind:       MessageKind;
  path?:      string;   // workspace-relative path, e.g. "src/web/app/page.tsx"
  content?:   string;   // full file content (write_file) or patch content
  start_line?: number;  // 0-based, patch_file only
  end_line?:   number;  // 0-based exclusive, patch_file only
  command?:   string;   // shell_start
  exit_code?: number;   // shell_done
  text?:      string;   // shell_output line
}

// ── Decorations ───────────────────────────────────────────────────────────────

const changedLineDecoration = vscode.window.createTextEditorDecorationType({
  backgroundColor: new vscode.ThemeColor('diffEditor.insertedLineBackground'),
  isWholeLine: true,
  overviewRulerColor: new vscode.ThemeColor('editorOverviewRuler.addedForeground'),
  overviewRulerLane: vscode.OverviewRulerLane.Right,
});

// ── Extension state ───────────────────────────────────────────────────────────

let socket:        WebSocket | null = null;
let statusBar:     vscode.StatusBarItem;
let outputChannel: vscode.OutputChannel;
let reconnectTimer: NodeJS.Timeout | null = null;
let reconnectAttempts = 0;
const MAX_RECONNECT = 20;
const RECONNECT_MS  = 2000;

// ── Activation ────────────────────────────────────────────────────────────────

export function activate(context: vscode.ExtensionContext) {
  outputChannel = vscode.window.createOutputChannel('JAVE Live');
  log('JAVE Live extension activated');

  statusBar = vscode.window.createStatusBarItem(vscode.StatusBarAlignment.Left, 100);
  statusBar.command = 'jave.connect';
  setStatus('disconnected');
  statusBar.show();

  context.subscriptions.push(
    vscode.commands.registerCommand('jave.connect',    () => startConnection(context)),
    vscode.commands.registerCommand('jave.disconnect', () => stopConnection()),
    vscode.commands.registerCommand('jave.showLog',    () => outputChannel.show()),
  );

  const cfg = vscode.workspace.getConfiguration('jave');
  if (cfg.get<boolean>('autoConnect')) {
    startConnection(context);
  }
}

export function deactivate() {
  stopConnection();
}

// ── Connection lifecycle ──────────────────────────────────────────────────────

function startConnection(context: vscode.ExtensionContext) {
  if (socket?.readyState === WebSocket.OPEN) {
    log('Already connected');
    return;
  }
  clearReconnectTimer();
  connect(context);
}

function connect(context: vscode.ExtensionContext) {
  const cfg = vscode.workspace.getConfiguration('jave');
  const port = cfg.get<number>('port') ?? 7779;
  const url = `ws://127.0.0.1:${port}`;

  log(`Connecting to JAVE bridge at ${url} …`);
  setStatus('connecting');

  try {
    // 1. Create a local constant to satisfy TypeScript's null-checking
    const ws = new WebSocket(url);
    socket = ws;

    // 2. Use the 'ws' constant to attach listeners
    ws.on('open', () => {
      reconnectAttempts = 0;
      setStatus('connected');
      log('✅  Connected to JAVE bridge');
    });

    ws.on('message', (raw: WebSocket.RawData) => {
      try {
        const msg: JaveMessage = JSON.parse(raw.toString());
        handleMessage(msg);
      } catch (err) {
        log(`Bad message from JAVE: ${err}`);
      }
    });

    ws.on('close', () => {
      setStatus('disconnected');
      log('Connection closed — will retry …');
      scheduleReconnect(context);
    });

    ws.on('error', (err: Error) => {
      // Use 'as any' to bypass the specific NodeJS.ErrnoException check
      if ((err as any).code !== 'ECONNREFUSED') {
        log(`Socket error: ${err.message}`);
      }
    });
  } catch (err) {
    log(`Failed to create socket: ${err}`);
    scheduleReconnect(context);
    return;
  }
}

function stopConnection() {
  clearReconnectTimer();
  if (socket) {
    socket.close();
    socket = null;
  }
  setStatus('disconnected');
  log('Disconnected');
}

function scheduleReconnect(context: vscode.ExtensionContext) {
  if (reconnectAttempts >= MAX_RECONNECT) {
    log('Max reconnect attempts reached. Use "JAVE: Connect" to retry manually.');
    setStatus('disconnected');
    return;
  }
  reconnectAttempts++;
  reconnectTimer = setTimeout(() => connect(context), RECONNECT_MS);
}

function clearReconnectTimer() {
  if (reconnectTimer) {
    clearTimeout(reconnectTimer);
    reconnectTimer = null;
  }
}

// ── Message handler ───────────────────────────────────────────────────────────

async function handleMessage(msg: JaveMessage) {
  switch (msg.kind) {
    case 'ping':
      return;

    case 'write_file':
      if (msg.path && msg.content !== undefined) {
        await writeFile(msg.path, msg.content);
      }
      break;

    case 'patch_file':
      if (msg.path && msg.content !== undefined &&
          msg.start_line !== undefined && msg.end_line !== undefined) {
        await patchFile(msg.path, msg.content, msg.start_line, msg.end_line);
      }
      break;

    case 'delete_file':
      if (msg.path) {
        await deleteFile(msg.path);
      }
      break;

    case 'make_dir':
      if (msg.path) {
        await makeDir(msg.path);
      }
      break;

    case 'shell_start':
      log(`▶  ${msg.command ?? '(shell command)'}`);
      setStatus('running', msg.command);
      break;

    case 'shell_done':
      const exitCode = msg.exit_code ?? 0;
      if (exitCode === 0) {
        log(`✅  Command finished (exit 0)`);
        setStatus('connected');
      } else {
        log(`❌  Command failed (exit ${exitCode})`);
        setStatus('error', `exit ${exitCode}`);
        setTimeout(() => setStatus('connected'), 4000);
      }
      break;

    case 'shell_output':
      if (msg.text) {
        outputChannel.appendLine(`  ${msg.text}`);
      }
      break;

    default:
      log(`Unknown message kind: ${(msg as JaveMessage).kind}`);
  }
}

// ── File operations ───────────────────────────────────────────────────────────

function resolveWorkspacePath(relativePath: string): string | null {
  const folders = vscode.workspace.workspaceFolders;
  if (!folders || folders.length === 0) {
    log('No workspace folder open — cannot write files');
    return null;
  }
  // Strip leading slash or "workspace/" prefix that JAVE adds.
  const clean = relativePath
    .replace(/^\/workspace\//, '')
    .replace(/^\//, '');
  return path.join(folders[0].uri.fsPath, clean);
}

async function writeFile(relativePath: string, content: string) {
  const absPath = resolveWorkspacePath(relativePath);
  if (!absPath) return;

  log(`✏️   write  ${relativePath}`);

  // Ensure parent directory exists.
  const dir = path.dirname(absPath);
  await fs.promises.mkdir(dir, { recursive: true });

  await fs.promises.writeFile(absPath, content, 'utf8');

  // Open and reveal in editor, then highlight all lines.
  const doc = await vscode.workspace.openTextDocument(absPath);
  const editor = await vscode.window.showTextDocument(doc, {
    preview: false,
    preserveFocus: true,
  });

  highlightLines(editor, 0, doc.lineCount - 1);
}

async function patchFile(
  relativePath: string,
  newContent: string,
  startLine: number,
  endLine: number,
) {
  const absPath = resolveWorkspacePath(relativePath);
  if (!absPath) return;

  log(`🔧  patch  ${relativePath}  lines ${startLine}–${endLine}`);

  const doc = await vscode.workspace.openTextDocument(absPath);
  const editor = await vscode.window.showTextDocument(doc, {
    preview: false,
    preserveFocus: true,
  });

  const edit = new vscode.WorkspaceEdit();
  const range = new vscode.Range(
    new vscode.Position(startLine, 0),
    new vscode.Position(Math.min(endLine, doc.lineCount), 0),
  );
  edit.replace(doc.uri, range, newContent + '\n');
  await vscode.workspace.applyEdit(edit);

  highlightLines(editor, startLine, startLine + newContent.split('\n').length - 1);
}

async function deleteFile(relativePath: string) {
  const absPath = resolveWorkspacePath(relativePath);
  if (!absPath) return;

  log(`🗑️   delete ${relativePath}`);
  try {
    await fs.promises.unlink(absPath);
  } catch {
    // File may not exist — not an error.
  }
}

async function makeDir(relativePath: string) {
  const absPath = resolveWorkspacePath(relativePath);
  if (!absPath) return;

  log(`📁  mkdir  ${relativePath}`);
  await fs.promises.mkdir(absPath, { recursive: true });
}

// ── Highlight helpers ─────────────────────────────────────────────────────────

function highlightLines(editor: vscode.TextEditor, startLine: number, endLine: number) {
  const cfg = vscode.workspace.getConfiguration('jave');
  const duration = cfg.get<number>('highlightDuration') ?? 3000;

  const ranges: vscode.Range[] = [];
  for (let i = startLine; i <= endLine; i++) {
    if (i < editor.document.lineCount) {
      ranges.push(editor.document.lineAt(i).range);
    }
  }

  editor.setDecorations(changedLineDecoration, ranges);

  // Scroll to the first changed line.
  if (ranges.length > 0) {
    editor.revealRange(ranges[0], vscode.TextEditorRevealType.InCenterIfOutsideViewport);
  }

  setTimeout(() => {
    editor.setDecorations(changedLineDecoration, []);
  }, duration);
}

// ── Status bar ────────────────────────────────────────────────────────────────

type StatusKind = 'disconnected' | 'connecting' | 'connected' | 'running' | 'error';

function setStatus(kind: StatusKind, detail?: string) {
  const labels: Record<StatusKind, string> = {
    disconnected: '$(circle-slash) JAVE',
    connecting:   '$(sync~spin) JAVE',
    connected:    '$(radio-tower) JAVE',
    running:      '$(loading~spin) JAVE',
    error:        '$(error) JAVE',
  };
  const tooltips: Record<StatusKind, string> = {
    disconnected: 'JAVE: not connected — click to connect',
    connecting:   'JAVE: connecting …',
    connected:    'JAVE: live — watching for changes',
    running:      `JAVE: running — ${detail ?? ''}`,
    error:        `JAVE: error — ${detail ?? ''}`,
  };
  statusBar.text    = labels[kind];
  statusBar.tooltip = tooltips[kind];
  statusBar.color   = kind === 'error' ? new vscode.ThemeColor('errorForeground') : undefined;
}

// ── Logging ───────────────────────────────────────────────────────────────────

function log(msg: string) {
  const ts = new Date().toLocaleTimeString();
  outputChannel.appendLine(`[${ts}] ${msg}`);
}