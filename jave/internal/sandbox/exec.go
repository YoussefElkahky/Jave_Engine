package sandbox

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"jave/internal/bridge"
	"jave/internal/config"
)

const (
	foregroundTimeout = 2 * time.Minute
	installTimeout    = 10 * time.Minute
)

// ── Command classification ────────────────────────────────────────────────────

func isBackgroundCommand(command string) bool {
	trimmed := strings.TrimSpace(command)
	return strings.HasSuffix(trimmed, "&") ||
		strings.Contains(trimmed, " & ") ||
		strings.HasPrefix(trimmed, "nohup ")
}

func looksLikeServer(command string) bool {
	lc := strings.ToLower(command)
	return strings.Contains(lc, "npm run dev") ||
		strings.Contains(lc, "npm start") ||
		strings.Contains(lc, "next dev") ||
		strings.Contains(lc, "next start") ||
		strings.Contains(lc, "./server") ||
		strings.Contains(lc, "./main") ||
		strings.Contains(lc, "go run") ||
		strings.Contains(lc, " air")
}

func looksLikeInstall(command string) bool {
	lc := strings.ToLower(command)
	return strings.Contains(lc, "npm install") ||
		strings.Contains(lc, "npm ci") ||
		strings.Contains(lc, "npx ") ||
		strings.Contains(lc, "pnpm install") ||
		strings.Contains(lc, "pnpm add") ||
		strings.Contains(lc, "yarn install") ||
		strings.Contains(lc, "yarn add") ||
		strings.Contains(lc, "go mod") ||
		strings.Contains(lc, "go get") ||
		strings.Contains(lc, "pip install") ||
		strings.Contains(lc, "apt install") ||
		strings.Contains(lc, "apt-get install")
}

func timeoutFor(command string) time.Duration {
	if looksLikeInstall(command) {
		return installTimeout
	}
	return foregroundTimeout
}

// ── Non-interactive rewriting ─────────────────────────────────────────────────

// makeNonInteractive rewrites each line of the command so that Node tools
// never prompt for input.
func makeNonInteractive(command string) string {
	lines := strings.Split(command, "\n")
	out := make([]string, 0, len(lines)+2)

	needsCI := false
	for _, l := range lines {
		lc := strings.ToLower(l)
		if strings.Contains(lc, "npx ") || strings.Contains(lc, "npm ") {
			needsCI = true
			break
		}
	}
	if needsCI {
		out = append(out, "export CI=true")
		out = append(out, "export NPM_CONFIG_YES=true")
	}

	for _, line := range lines {
		line = rewriteCreateNextApp(line)
		line = rewriteFramerMotion(line)
		out = append(out, line)
	}

	return strings.Join(out, "\n")
}

func rewriteCreateNextApp(line string) string {
	const marker = "create-next-app"
	if !strings.Contains(line, marker) {
		return line
	}

	fields := strings.Fields(line)
	insertAt := -1
	for i, f := range fields {
		if strings.HasPrefix(f, marker) {
			insertAt = i + 1
			break
		}
	}
	if insertAt < 0 {
		return line
	}

	rest := fields[insertAt:]
	hasPath := false
	for _, f := range rest {
		if !strings.HasPrefix(f, "-") {
			hasPath = true
			break
		}
	}

	inject := []string{}
	if !strings.Contains(line, "--yes") {
		inject = append(inject, "--yes")
	}
	if !strings.Contains(line, "--no-turbopack") && !strings.Contains(line, "--turbopack") {
		inject = append(inject, "--no-turbopack")
	}

	result := make([]string, 0, len(fields)+len(inject)+1)
	result = append(result, fields[:insertAt]...)
	result = append(result, inject...)
	if !hasPath {
		result = append(result, ".")
	}
	result = append(result, fields[insertAt:]...)
	return strings.Join(result, " ")
}

func rewriteFramerMotion(line string) string {
	line = strings.ReplaceAll(line, "@framer-motion/nextjs", "framer-motion")
	line = strings.ReplaceAll(line, "@framer-motion/react", "framer-motion")
	line = strings.ReplaceAll(line, "@framer-motion/client", "framer-motion")
	line = strings.ReplaceAll(line, "@framer-motion/", "framer-motion ")
	line = strings.ReplaceAll(line, "@framer/motion", "framer-motion")
	return line
}

// ── Path / command rewriting ──────────────────────────────────────────────────

func rewriteHostWorkspacePaths(command string) string {
	hostRoot := strings.TrimRight(config.WorkspaceDir, string(os.PathSeparator))
	if hostRoot == "" {
		return command
	}
	command = strings.ReplaceAll(command, hostRoot+string(os.PathSeparator), "/workspace/")
	command = strings.ReplaceAll(command, hostRoot, "/workspace")
	return command
}

func consolidateCDs(command string) string {
	lines := strings.Split(command, "\n")
	result := make([]string, 0, len(lines))
	pending := ""
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "cd ") && !strings.Contains(trimmed, "&&") {
			if pending != "" {
				result = append(result, pending)
			}
			pending = trimmed
		} else {
			if pending != "" {
				result = append(result, pending+" && "+trimmed)
				pending = ""
			} else {
				result = append(result, trimmed)
			}
		}
	}
	if pending != "" {
		result = append(result, pending)
	}
	return strings.Join(result, "\n")
}

func fixDirOverFileConflicts(command string) string {
	lines := strings.Split(command, "\n")
	result := make([]string, 0, len(lines))

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "mkdir") && strings.Contains(trimmed, "-p") {
			fields := strings.Fields(trimmed)
			fixed := make([]string, 0, len(fields))
			changed := false
			for _, f := range fields {
				if strings.HasPrefix(f, "/") || strings.HasPrefix(f, "./") || strings.HasPrefix(f, "../") {
					if ext := filepath.Ext(f); ext != "" {
						parent := filepath.Dir(f)
						fixed = append(fixed, parent)
						changed = true
						continue
					}
				}
				fixed = append(fixed, f)
			}
			if changed {
				fmt.Printf("  ⚠️   mkdir target looks like a file path — using parent dir instead.\n")
				line = strings.Join(fixed, " ")
			}
		}

		if strings.Contains(trimmed, "<<") && (strings.Contains(trimmed, " > ") || strings.Contains(trimmed, " >> ")) {
			parts := strings.Fields(trimmed)
			for i, p := range parts {
				if (p == ">" || p == ">>") && i+1 < len(parts) {
					outPath := parts[i+1]
					if strings.HasPrefix(outPath, "/") || strings.HasPrefix(outPath, "./") {
						guard := fmt.Sprintf("rm -rf %s 2>/dev/null || true", outPath)
						if !strings.Contains(line, "rm -rf "+outPath) {
							line = guard + "\n" + line
						}
					}
					break
				}
			}
		}

		result = append(result, line)
	}
	return strings.Join(result, "\n")
}

func cleanCommand(command string) string {
	lines := strings.Split(command, "\n")
	result := make([]string, 0, len(lines))
	insideHeredoc := false
	for _, line := range lines {
		if insideHeredoc {
			result = append(result, line)
			if strings.TrimSpace(line) == "EOF" {
				insideHeredoc = false
			}
			continue
		}
		cleaned := strings.TrimSpace(line)
		cleaned = strings.TrimPrefix(cleaned, "$ ")
		cleaned = strings.TrimPrefix(cleaned, "# ")
		result = append(result, cleaned)
		if strings.Contains(cleaned, "<<") && strings.Contains(cleaned, "EOF") {
			insideHeredoc = true
		}
	}
	return strings.TrimSpace(strings.Join(result, "\n"))
}

// ── VS Code live-sync: parse heredoc file writes ──────────────────────────────
//
// After a successful sandbox run we walk the executed command and for every
//   cat << 'EOF' > /workspace/some/file.ext  …content…  EOF
// block we extract the path and content and push a write_file message to the
// VS Code extension so the editor updates in real time.
//
// We also handle:
//   mkdir -p /workspace/…   → make_dir
//   rm …/path               → delete_file  (best-effort)

func syncCommandToVSCode(command string) {
	lines := strings.Split(command, "\n")

	type heredoc struct {
		path  string
		lines []string
	}

	var current *heredoc

	for _, raw := range lines {
		line := strings.TrimRight(raw, "\r")

		// ── Inside a heredoc block ────────────────────────────────────────────
		if current != nil {
			if strings.TrimSpace(line) == "EOF" {
				// End of heredoc — send the file to VS Code.
				content := strings.Join(current.lines, "\n")
				vscodePath := toVSCodePath(current.path)
				bridge.SendWriteFile(vscodePath, content)
				current = nil
			} else {
				current.lines = append(current.lines, line)
			}
			continue
		}

		trimmed := strings.TrimSpace(line)

		// ── cat << 'EOF' > /workspace/… ──────────────────────────────────────
		if strings.Contains(trimmed, "<<") &&
			(strings.Contains(trimmed, "EOF") || strings.Contains(trimmed, "'EOF'") || strings.Contains(trimmed, `"EOF"`)) &&
			(strings.Contains(trimmed, ">") || strings.Contains(trimmed, ">>")) {

			outPath := extractHeredocTarget(trimmed)
			if outPath != "" {
				current = &heredoc{path: outPath, lines: []string{}}
			}
			continue
		}

		// ── mkdir -p /workspace/… ─────────────────────────────────────────────
		if strings.HasPrefix(trimmed, "mkdir") {
			fields := strings.Fields(trimmed)
			for _, f := range fields {
				if strings.HasPrefix(f, "/workspace/") {
					bridge.SendMakeDir(toVSCodePath(f))
				}
			}
			continue
		}

		// ── rm / rm -rf /workspace/… ──────────────────────────────────────────
		if strings.HasPrefix(trimmed, "rm ") {
			fields := strings.Fields(trimmed)
			for _, f := range fields {
				if strings.HasPrefix(f, "/workspace/") {
					bridge.SendDeleteFile(toVSCodePath(f))
				}
			}
		}
	}
}

// extractHeredocTarget parses lines like:
//
//	cat << 'EOF' > /workspace/src/app/page.tsx
//	cat << EOF >> /workspace/tailwind.config.ts
//
// and returns the output file path, or "" if it cannot be found.
func extractHeredocTarget(line string) string {
	// Find > or >> token and take the next word.
	fields := strings.Fields(line)
	for i, f := range fields {
		if (f == ">" || f == ">>") && i+1 < len(fields) {
			p := fields[i+1]
			// Strip trailing semicolons or comments that sometimes appear.
			p = strings.TrimRight(p, ";")
			if strings.HasPrefix(p, "/") || strings.HasPrefix(p, "./") {
				return p
			}
		}
	}
	return ""
}

// toVSCodePath converts an in-sandbox absolute path to a workspace-relative
// path the VS Code extension can handle.
//
// /workspace/src/app/page.tsx  →  src/app/page.tsx
// /workspace/package.json      →  package.json
func toVSCodePath(sandboxPath string) string {
	p := strings.TrimPrefix(sandboxPath, "/workspace/")
	p = strings.TrimPrefix(p, "/workspace")
	p = strings.TrimPrefix(p, "./")
	return p
}

// ── Main entry point ──────────────────────────────────────────────────────────

// RunBlockWithConfirm prepares a shell block, asks for confirmation, executes
// it inside the sandbox, streams output to the terminal AND to VS Code, and
// after a successful run pushes all file changes to VS Code.
func RunBlockWithConfirm(command string, askConfirm func() bool) error {
	command = cleanCommand(command)
	command = rewriteHostWorkspacePaths(command)
	command = fixDirOverFileConflicts(command)
	command = consolidateCDs(command)

	if strings.TrimSpace(command) == "" {
		return nil
	}

	if looksLikeServer(command) && !isBackgroundCommand(command) {
		fmt.Println("\n  ⚠️   Server/watcher detected — adding & to run in background.")
		command = command + " &"
	}

	command = makeNonInteractive(command)
	command = "cd /workspace\n" + command

	timeout := timeoutFor(command)

	fmt.Printf("\n🧰  Sandbox command (writes to: %s):\n%s\n", config.WorkspaceDir, command)
	if isBackgroundCommand(command) {
		fmt.Println("  ℹ️   Background process — JAVE will not wait for it.")
	} else {
		fmt.Printf("  ℹ️   Timeout: %s\n", timeout)
	}

	if !askConfirm() {
		fmt.Println("  ⏭️   Skipped.")
		return nil
	}

	// Notify VS Code: command starting.
	bridge.SendShellStart(command)

	var runErr error
	if isBackgroundCommand(command) {
		runErr = runDetached(command)
	} else {
		runErr = runWithTimeout(command, timeout)
	}

	exitCode := 0
	if runErr != nil {
		exitCode = 1
	}

	// Notify VS Code: command done.
	bridge.SendShellDone(exitCode)

	// On success, sync every file write in the command to VS Code.
	if runErr == nil {
		syncCommandToVSCode(command)
	}

	return runErr
}

// ── Execution ─────────────────────────────────────────────────────────────────

// runWithTimeout executes the command inside the sandbox and streams its
// stdout/stderr directly to the terminal AND to VS Code line-by-line.
func runWithTimeout(command string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	sandboxCmd := exec.CommandContext(ctx,
		"docker", "exec",
		config.SandboxContainer,
		"bash", "-c", command,
	)

	devNull, err := os.Open(os.DevNull)
	if err != nil {
		return fmt.Errorf("open /dev/null: %w", err)
	}
	defer devNull.Close()
	sandboxCmd.Stdin = devNull

	stdoutPipe, err := sandboxCmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	stderrPipe, err := sandboxCmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("stderr pipe: %w", err)
	}

	if err := sandboxCmd.Start(); err != nil {
		return fmt.Errorf("start docker exec: %w", err)
	}

	// streamLines copies a reader line-by-line to the terminal and VS Code.
	streamLines := func(r io.Reader, w io.Writer, done chan<- struct{}) {
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			line := scanner.Text()
			fmt.Fprintln(w, line)
			bridge.SendShellOutput(line)
		}
		done <- struct{}{}
	}

	done := make(chan struct{}, 2)
	go streamLines(stdoutPipe, os.Stdout, done)
	go streamLines(stderrPipe, os.Stderr, done)

	<-done
	<-done

	if err := sandboxCmd.Wait(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("⏱️  command timed out after %s\n"+
				"  Inspect: docker exec -it %s bash", timeout, config.SandboxContainer)
		}
		return fmt.Errorf("exec failed: %w", err)
	}

	fmt.Printf("\n  ✅  Done  →  %s\n", config.WorkspaceDir)
	return nil
}

func runDetached(command string) error {
	sandboxCmd := exec.Command(
		"docker", "exec", "-d",
		config.SandboxContainer,
		"bash", "-c", command,
	)
	if err := sandboxCmd.Run(); err != nil {
		return fmt.Errorf("detached exec failed: %w", err)
	}
	fmt.Println("  ✅  Started in background.")
	fmt.Println("  ℹ️   Logs: make sandbox-logs")
	return nil
}