package bridge

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// ── Protocol types (mirror extension.ts) ─────────────────────────────────────

type MessageKind string

const (
	KindWriteFile  MessageKind = "write_file"
	KindPatchFile  MessageKind = "patch_file"
	KindDeleteFile MessageKind = "delete_file"
	KindMakeDir    MessageKind = "make_dir"
	KindShellStart MessageKind = "shell_start"
	KindShellDone  MessageKind = "shell_done"
	KindShellOut   MessageKind = "shell_output"
	KindPing       MessageKind = "ping"
)

// OutMessage is the JSON payload sent to VS Code.
type OutMessage struct {
	Kind      MessageKind `json:"kind"`
	Path      string      `json:"path,omitempty"`
	Content   string      `json:"content,omitempty"`
	StartLine *int        `json:"start_line,omitempty"` // patch_file: 0-based
	EndLine   *int        `json:"end_line,omitempty"`   // patch_file: exclusive
	Command   string      `json:"command,omitempty"`    // shell_start
	ExitCode  *int        `json:"exit_code,omitempty"`  // shell_done
	Text      string      `json:"text,omitempty"`       // shell_output
}

// ── Connection registry ───────────────────────────────────────────────────────

var (
	upgrader = websocket.Upgrader{
		CheckOrigin:     func(r *http.Request) bool { return true },
		ReadBufferSize:  1024,
		WriteBufferSize: 1024 * 64,
	}

	mu    sync.Mutex
	conns = make(map[*websocket.Conn]struct{})
)

func registerConn(c *websocket.Conn) {
	mu.Lock()
	conns[c] = struct{}{}
	mu.Unlock()
}

func unregisterConn(c *websocket.Conn) {
	mu.Lock()
	delete(conns, c)
	mu.Unlock()
	c.Close()
}

// ── Server ────────────────────────────────────────────────────────────────────

// StartServer launches the WebSocket bridge on the given port and a background
// ping ticker to keep connections alive through proxies / firewalls.
func StartServer(port int) {
	http.HandleFunc("/", handleWS)
	go http.ListenAndServe(fmt.Sprintf(":%d", port), nil) //nolint:errcheck

	// Keepalive: send a ping every 15 s so the extension's WS stays open.
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			broadcast(&OutMessage{Kind: KindPing})
		}
	}()
}

func handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	registerConn(conn)
	fmt.Printf("\n  🔌  VS Code Connected to JAVE Live\n\nJAVE > ")

	// Drain inbound frames so the connection does not stall; we never
	// expect messages from the extension but the WS protocol requires reads.
	go func() {
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				unregisterConn(conn)
				return
			}
		}
	}()
}

// ── Broadcast helpers ─────────────────────────────────────────────────────────

func broadcast(msg *OutMessage) {
	data, err := json.Marshal(msg)
	if err != nil {
		return
	}

	mu.Lock()
	defer mu.Unlock()

	for c := range conns {
		if writeErr := c.WriteMessage(websocket.TextMessage, data); writeErr != nil {
			// Dead connection — remove it (cannot call unregisterConn while
			// holding the lock, so close and delete inline).
			c.Close()
			delete(conns, c)
		}
	}
}

// ── Public API used by exec.go and main.go ────────────────────────────────────

// SendWriteFile tells VS Code to create or overwrite a file.
func SendWriteFile(workspacePath, content string) {
	broadcast(&OutMessage{
		Kind:    KindWriteFile,
		Path:    workspacePath,
		Content: content,
	})
}

// SendPatchFile tells VS Code to replace lines [startLine, endLine) with newContent.
// startLine and endLine are 0-based; endLine is exclusive.
func SendPatchFile(workspacePath, newContent string, startLine, endLine int) {
	broadcast(&OutMessage{
		Kind:      KindPatchFile,
		Path:      workspacePath,
		Content:   newContent,
		StartLine: &startLine,
		EndLine:   &endLine,
	})
}

// SendDeleteFile tells VS Code to delete a file.
func SendDeleteFile(workspacePath string) {
	broadcast(&OutMessage{Kind: KindDeleteFile, Path: workspacePath})
}

// SendMakeDir tells VS Code to create a directory.
func SendMakeDir(workspacePath string) {
	broadcast(&OutMessage{Kind: KindMakeDir, Path: workspacePath})
}

// SendShellStart notifies VS Code that a sandbox command is about to run.
func SendShellStart(command string) {
	broadcast(&OutMessage{Kind: KindShellStart, Command: command})
}

// SendShellOutput streams a single line of sandbox stdout/stderr to VS Code.
func SendShellOutput(line string) {
	broadcast(&OutMessage{Kind: KindShellOut, Text: line})
}

// SendShellDone notifies VS Code that a sandbox command finished.
func SendShellDone(exitCode int) {
	broadcast(&OutMessage{Kind: KindShellDone, ExitCode: &exitCode})
}

// SendMessage is kept for backwards compatibility with any existing callers.
// Prefer the typed helpers above.
func SendMessage(kind string, path string, content string) {
	broadcast(&OutMessage{
		Kind:    MessageKind(kind),
		Path:    path,
		Content: content,
	})
}