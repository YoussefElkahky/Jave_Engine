"use strict";
var __createBinding = (this && this.__createBinding) || (Object.create ? (function(o, m, k, k2) {
    if (k2 === undefined) k2 = k;
    var desc = Object.getOwnPropertyDescriptor(m, k);
    if (!desc || ("get" in desc ? !m.__esModule : desc.writable || desc.configurable)) {
      desc = { enumerable: true, get: function() { return m[k]; } };
    }
    Object.defineProperty(o, k2, desc);
}) : (function(o, m, k, k2) {
    if (k2 === undefined) k2 = k;
    o[k2] = m[k];
}));
var __setModuleDefault = (this && this.__setModuleDefault) || (Object.create ? (function(o, v) {
    Object.defineProperty(o, "default", { enumerable: true, value: v });
}) : function(o, v) {
    o["default"] = v;
});
var __importStar = (this && this.__importStar) || (function () {
    var ownKeys = function(o) {
        ownKeys = Object.getOwnPropertyNames || function (o) {
            var ar = [];
            for (var k in o) if (Object.prototype.hasOwnProperty.call(o, k)) ar[ar.length] = k;
            return ar;
        };
        return ownKeys(o);
    };
    return function (mod) {
        if (mod && mod.__esModule) return mod;
        var result = {};
        if (mod != null) for (var k = ownKeys(mod), i = 0; i < k.length; i++) if (k[i] !== "default") __createBinding(result, mod, k[i]);
        __setModuleDefault(result, mod);
        return result;
    };
})();
var __importDefault = (this && this.__importDefault) || function (mod) {
    return (mod && mod.__esModule) ? mod : { "default": mod };
};
Object.defineProperty(exports, "__esModule", { value: true });
exports.activate = activate;
exports.deactivate = deactivate;
const vscode = __importStar(require("vscode"));
const ws_1 = __importDefault(require("ws"));
const path = __importStar(require("path"));
const fs = __importStar(require("fs"));
// ── Decorations ───────────────────────────────────────────────────────────────
const changedLineDecoration = vscode.window.createTextEditorDecorationType({
    backgroundColor: new vscode.ThemeColor('diffEditor.insertedLineBackground'),
    isWholeLine: true,
    overviewRulerColor: new vscode.ThemeColor('editorOverviewRuler.addedForeground'),
    overviewRulerLane: vscode.OverviewRulerLane.Right,
});
// ── Extension state ───────────────────────────────────────────────────────────
let socket = null;
let statusBar;
let outputChannel;
let reconnectTimer = null;
let reconnectAttempts = 0;
const MAX_RECONNECT = 20;
const RECONNECT_MS = 2000;
// ── Activation ────────────────────────────────────────────────────────────────
function activate(context) {
    outputChannel = vscode.window.createOutputChannel('JAVE Live');
    log('JAVE Live extension activated');
    statusBar = vscode.window.createStatusBarItem(vscode.StatusBarAlignment.Left, 100);
    statusBar.command = 'jave.connect';
    setStatus('disconnected');
    statusBar.show();
    context.subscriptions.push(vscode.commands.registerCommand('jave.connect', () => startConnection(context)), vscode.commands.registerCommand('jave.disconnect', () => stopConnection()), vscode.commands.registerCommand('jave.showLog', () => outputChannel.show()));
    const cfg = vscode.workspace.getConfiguration('jave');
    if (cfg.get('autoConnect')) {
        startConnection(context);
    }
}
function deactivate() {
    stopConnection();
}
// ── Connection lifecycle ──────────────────────────────────────────────────────
function startConnection(context) {
    if (socket?.readyState === ws_1.default.OPEN) {
        log('Already connected');
        return;
    }
    clearReconnectTimer();
    connect(context);
}
function connect(context) {
    const cfg = vscode.workspace.getConfiguration('jave');
    const port = cfg.get('port') ?? 7779;
    const url = `ws://127.0.0.1:${port}`;
    log(`Connecting to JAVE bridge at ${url} …`);
    setStatus('connecting');
    try {
        // 1. Create a local constant to satisfy TypeScript's null-checking
        const ws = new ws_1.default(url);
        socket = ws;
        // 2. Use the 'ws' constant to attach listeners
        ws.on('open', () => {
            reconnectAttempts = 0;
            setStatus('connected');
            log('✅  Connected to JAVE bridge');
        });
        ws.on('message', (raw) => {
            try {
                const msg = JSON.parse(raw.toString());
                handleMessage(msg);
            }
            catch (err) {
                log(`Bad message from JAVE: ${err}`);
            }
        });
        ws.on('close', () => {
            setStatus('disconnected');
            log('Connection closed — will retry …');
            scheduleReconnect(context);
        });
        ws.on('error', (err) => {
            // Use 'as any' to bypass the specific NodeJS.ErrnoException check
            if (err.code !== 'ECONNREFUSED') {
                log(`Socket error: ${err.message}`);
            }
        });
    }
    catch (err) {
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
function scheduleReconnect(context) {
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
async function handleMessage(msg) {
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
            }
            else {
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
            log(`Unknown message kind: ${msg.kind}`);
    }
}
// ── File operations ───────────────────────────────────────────────────────────
function resolveWorkspacePath(relativePath) {
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
async function writeFile(relativePath, content) {
    const absPath = resolveWorkspacePath(relativePath);
    if (!absPath)
        return;
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
async function patchFile(relativePath, newContent, startLine, endLine) {
    const absPath = resolveWorkspacePath(relativePath);
    if (!absPath)
        return;
    log(`🔧  patch  ${relativePath}  lines ${startLine}–${endLine}`);
    const doc = await vscode.workspace.openTextDocument(absPath);
    const editor = await vscode.window.showTextDocument(doc, {
        preview: false,
        preserveFocus: true,
    });
    const edit = new vscode.WorkspaceEdit();
    const range = new vscode.Range(new vscode.Position(startLine, 0), new vscode.Position(Math.min(endLine, doc.lineCount), 0));
    edit.replace(doc.uri, range, newContent + '\n');
    await vscode.workspace.applyEdit(edit);
    highlightLines(editor, startLine, startLine + newContent.split('\n').length - 1);
}
async function deleteFile(relativePath) {
    const absPath = resolveWorkspacePath(relativePath);
    if (!absPath)
        return;
    log(`🗑️   delete ${relativePath}`);
    try {
        await fs.promises.unlink(absPath);
    }
    catch {
        // File may not exist — not an error.
    }
}
async function makeDir(relativePath) {
    const absPath = resolveWorkspacePath(relativePath);
    if (!absPath)
        return;
    log(`📁  mkdir  ${relativePath}`);
    await fs.promises.mkdir(absPath, { recursive: true });
}
// ── Highlight helpers ─────────────────────────────────────────────────────────
function highlightLines(editor, startLine, endLine) {
    const cfg = vscode.workspace.getConfiguration('jave');
    const duration = cfg.get('highlightDuration') ?? 3000;
    const ranges = [];
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
function setStatus(kind, detail) {
    const labels = {
        disconnected: '$(circle-slash) JAVE',
        connecting: '$(sync~spin) JAVE',
        connected: '$(radio-tower) JAVE',
        running: '$(loading~spin) JAVE',
        error: '$(error) JAVE',
    };
    const tooltips = {
        disconnected: 'JAVE: not connected — click to connect',
        connecting: 'JAVE: connecting …',
        connected: 'JAVE: live — watching for changes',
        running: `JAVE: running — ${detail ?? ''}`,
        error: `JAVE: error — ${detail ?? ''}`,
    };
    statusBar.text = labels[kind];
    statusBar.tooltip = tooltips[kind];
    statusBar.color = kind === 'error' ? new vscode.ThemeColor('errorForeground') : undefined;
}
// ── Logging ───────────────────────────────────────────────────────────────────
function log(msg) {
    const ts = new Date().toLocaleTimeString();
    outputChannel.appendLine(`[${ts}] ${msg}`);
}
//# sourceMappingURL=extension.js.map