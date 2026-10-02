package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"jave/internal/bridge"
	"jave/internal/config"
	"jave/internal/ollama"
	"jave/internal/sandbox"
	"jave/internal/status"
	
)

const systemPrompt = `### WHO YOU ARE
You are JAVE — a Senior Full-Stack Architect running on Kali Linux.
You build modern, production-ready web applications. You never produce
basic landing pages. Every UI you generate is motion-first, visually
distinguished, and immediately deployable.

### YOUR TECH STACK
Frontend  : Next.js 14+ (App Router), TypeScript, Tailwind CSS
Motion    : Framer Motion — package name is ALWAYS "framer-motion", never @framer/motion or @framer-motion/anything
            GSAP (complex timelines),
            Three.js / React Three Fiber (3D scenes)
UI Style  : Glassmorphism, bento-grid layouts, scroll-triggered reveals,
            dark-mode-first, micro-interactions on every interactive element
Backend   : Go (net/http or Fiber), REST or tRPC
Database  : PostgreSQL with Prisma ORM, or SQLite for local-first apps
Auth      : NextAuth.js v5 or Clerk
DevOps    : Docker, Vercel CLI, GitHub Actions

### ARCHITECT PROTOCOL — follow this order for every request
1. SCAN   — read the project tree and key files provided. Never invent paths.
2. PLAN   — write a short blueprint (3-5 lines):
            • Files created or modified
            • Motion / design choices
            • Dependencies to install
            • Any risk to existing routes or state
3. BUILD  — emit all shell commands inside ` + "```bash" + ` blocks.

### SHELL RULES — non-negotiable
- ALL files via:      cat << 'EOF' > path/to/file.tsx
- Directories via:    mkdir -p path/to/dir
- Packages via:       npm install package-name
- Never use nano, vim, or any interactive editor.
- Never use go run for production; use go build then run the binary.
- One ` + "```bash" + ` block per logical step so the user can approve each.
- ALWAYS use /workspace/ paths. Never use host-absolute paths.

### CODE QUALITY
- TypeScript strict mode always. No 'any'.
- Descriptive variable names — never single letters except loop counters.
- Components in src/components/, pages in src/app/, utils in src/lib/.
- Every animation must have a reduced-motion fallback.
- Mobile-responsive by default (Tailwind responsive prefixes).`

const (
	symbolOK   = "  ✅"
	symbolFail = "  ❌"
	symbolWarn = "  ⚠️ "
	symbolInfo = "  ℹ️ "
)

func main() {
	result := status.RunAll()
	if result.FatalErrors {
		os.Exit(1)
	}

	bridge.StartServer(7779)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Ctrl+C → clean shutdown.
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		fmt.Println("\n\nShutting down JAVE...")
		cancel()
		time.Sleep(150 * time.Millisecond)
		os.Exit(0)
	}()

	ollamaClient := ollama.NewClient()
	history := []ollama.Message{{Role: "system", Content: systemPrompt}}

	// ── Single sequential stdin reader ───────────────────────────────────────
	//
	// ALL stdin reads happen here, in the main goroutine, sequentially.
	// There is no background scanner goroutine and no needConfirm/sandboxDone
	// channels. The previous channel-based design had a fundamental race:
	//
	//   1. Main loop: scanner.Scan() blocks waiting for user input.
	//   2. User types prompt → handleInput() is called.
	//   3. handleInput() sends needConfirm signal.
	//   4. But the scanner goroutine is ALREADY blocked on its next Scan()
	//      call (it looped back before needConfirm arrived), so it misses the
	//      signal and reads the user's "y" as a new prompt.
	//
	// Fix: pass the scanner into handleInput(). When confirmation is needed,
	// handleInput calls scanner.Scan() directly. Since the main loop is
	// blocked waiting for handleInput to return, there is exactly one reader
	// at all times — zero races possible.

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	fmt.Println("\n🏗️  JAVE ready. Type a request, 'status', 'history', 'reset', 'tree', or 'quit'.")

	for {
		if ctx.Err() != nil {
			return
		}

		fmt.Print("\nJAVE > ")

		if !scanner.Scan() {
			// EOF or Ctrl+D
			fmt.Println("\nShutting down JAVE...")
			return
		}

		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if line == "quit" || line == "exit" {
			fmt.Println("Shutting down JAVE...")
			return
		}

		history = handleInput(ctx, ollamaClient, scanner, line, history)
	}
}

// ── Input handler ─────────────────────────────────────────────────────────────

// handleInput processes one user turn end-to-end:
//  1. Built-in commands (status, history, reset, tree)
//  2. Stream a response from Ollama
//  3. For each shell block in the response, ask "Execute? (y/n)" by calling
//     scanner.Scan() directly — the same scanner the main loop owns, used
//     synchronously, so there is never more than one concurrent stdin reader.
func handleInput(
	ctx context.Context,
	ollamaClient *ollama.Client,
	scanner *bufio.Scanner,
	userInput string,
	history []ollama.Message,
) []ollama.Message {

	switch userInput {
	case "status":
		runPreFlight()
		return history
	case "history":
		fmt.Println("\n── Conversation history ──")
		for i, msg := range history {
			if msg.Role == "system" {
				continue
			}
			label := "YOU "
			if msg.Role == "assistant" {
				label = "JAVE"
			}
			preview := msg.Content
			if len(preview) > 120 {
				preview = preview[:120] + "…"
			}
			fmt.Printf("[%d] %s: %s\n", i, label, preview)
		}
		fmt.Println("──────────────────────────")
		return history
	case "reset":
		fmt.Println("🔄  Conversation reset.")
		return []ollama.Message{{Role: "system", Content: systemPrompt}}
	case "tree":
		fmt.Println(getProjectTree())
		return history
	}

	// ── Build prompt ──────────────────────────────────────────────────────────

	projectContext := buildProjectContext()
	fullUserMessage := projectContext + "\n\n### REQUEST\n" + userInput
	history = append(history, ollama.Message{Role: "user", Content: fullUserMessage})

	// ── Stream response ───────────────────────────────────────────────────────

	fmt.Print("\n🤖 JAVE: ")

	tokenChan := make(chan string, 64)
	var responseBuilder strings.Builder
	streamErr := make(chan error, 1)

	go func() {
		defer close(tokenChan)
		streamErr <- ollamaClient.ChatStream(ctx, history, tokenChan)
	}()

	for token := range tokenChan {
		fmt.Print(token)
		responseBuilder.WriteString(token)
	}
	fmt.Println()

	if err := <-streamErr; err != nil {
		if ctx.Err() != nil {
			return history
		}
		fmt.Printf("\n%s  Stream error: %v\n", symbolFail, err)
		fmt.Printf("%s  Run 'status' to recheck all systems.\n", symbolInfo)
		return history
	}

	if ctx.Err() != nil {
		return history
	}

	assistantReply := responseBuilder.String()
	if strings.TrimSpace(assistantReply) == "" {
		fmt.Printf("\n%s  JAVE returned an empty response.\n", symbolWarn)
		fmt.Printf("%s  The model may still be loading. Try again in a moment.\n", symbolInfo)
		return history
	}

	history = append(history, ollama.Message{Role: "assistant", Content: assistantReply})

	// ── Execute shell blocks ──────────────────────────────────────────────────

	shellBlocks := extractShellBlocks(assistantReply)
	if len(shellBlocks) == 0 {
		return history
	}

	for i, block := range shellBlocks {
		fmt.Printf("\n%s  Block %d of %d\n", symbolInfo, i+1, len(shellBlocks))

		// askConfirm reads one line from stdin synchronously.
		// The main loop is blocked inside handleInput right now, so this is
		// the only active reader — no race with the outer loop.
		askConfirm := func() bool {
			fmt.Print("Execute? (y/n): ")
			if !scanner.Scan() {
				return false
			}
			ans := strings.TrimSpace(strings.ToLower(scanner.Text()))
			return ans == "y" || ans == "yes"
		}

		err := sandbox.RunBlockWithConfirm(block, askConfirm)
		if err != nil {
			fmt.Printf("%s  Block %d failed: %v\n", symbolFail, i+1, err)
			fmt.Printf("%s  Remaining blocks skipped.\n", symbolWarn)
			break
		}
	}

	return history
}

// ── Pre-flight ────────────────────────────────────────────────────────────────

type preflightCheck struct {
	name   string
	status string
	detail string
	fatal  bool
}

func runPreFlight() bool {
	fmt.Println("\n╔══════════════════════════════════════════════╗")
	fmt.Println("║          JAVE  Pre-Flight Check              ║")
	fmt.Println("╚══════════════════════════════════════════════╝")
	checks := []preflightCheck{
		checkDocker(), checkSandbox(), checkWorkspace(), checkOllama(), checkModel(),
	}
	allGood := true
	for _, c := range checks {
		fmt.Printf("%s  %-22s %s\n", c.status, c.name, c.detail)
		if c.fatal && c.status == symbolFail {
			allGood = false
		}
	}
	fmt.Println("──────────────────────────────────────────────")
	if !allGood {
		fmt.Println(symbolFail + "  One or more required services are down.")
		return false
	}
	fmt.Println(symbolOK + "  All systems go.")
	return true
}

func checkDocker() preflightCheck {
	if err := exec.Command("docker", "info").Run(); err != nil {
		return preflightCheck{"Docker daemon", symbolFail, "not running  →  sudo systemctl start docker", true}
	}
	return preflightCheck{"Docker daemon", symbolOK, "running", false}
}

func checkSandbox() preflightCheck {
	out, err := exec.Command("docker", "inspect", "--format={{.State.Running}}", config.SandboxContainer).Output()
	if err != nil || strings.TrimSpace(string(out)) != "true" {
		return preflightCheck{"Sandbox container", symbolFail,
			fmt.Sprintf("%q not running  →  make sandbox-start", config.SandboxContainer), true}
	}
	if err := exec.Command("docker", "exec", config.SandboxContainer,
		"bash", "-c", "touch /workspace/.jave_probe && rm /workspace/.jave_probe").Run(); err != nil {
		return preflightCheck{"Sandbox /workspace", symbolFail, "mount not writable  →  make sandbox-restart", true}
	}
	return preflightCheck{"Sandbox container", symbolOK,
		fmt.Sprintf("%q  →  /workspace writable", config.SandboxContainer), false}
}

func checkWorkspace() preflightCheck {
	info, err := os.Stat(config.WorkspaceDir)
	if err != nil {
		if mkErr := os.MkdirAll(config.WorkspaceDir, 0755); mkErr != nil {
			return preflightCheck{"Workspace dir", symbolFail,
				fmt.Sprintf("cannot create %s: %v", config.WorkspaceDir, mkErr), true}
		}
		return preflightCheck{"Workspace dir", symbolWarn,
			fmt.Sprintf("created %s (was missing)", config.WorkspaceDir), false}
	}
	if !info.IsDir() {
		return preflightCheck{"Workspace dir", symbolFail,
			fmt.Sprintf("%s is not a directory", config.WorkspaceDir), true}
	}
	return preflightCheck{"Workspace dir", symbolOK, config.WorkspaceDir, false}
}

func checkOllama() preflightCheck {
	c := &http.Client{Timeout: 4 * time.Second}
	resp, err := c.Get(config.OllamaAPI + "/api/tags")
	if err != nil {
		return preflightCheck{"Ollama service", symbolFail,
			fmt.Sprintf("unreachable at %s  →  ollama serve", config.OllamaAPI), true}
	}
	resp.Body.Close()
	return preflightCheck{"Ollama service", symbolOK, config.OllamaAPI, false}
}

func checkModel() preflightCheck {
	c := &http.Client{Timeout: 4 * time.Second}
	resp, err := c.Get(config.OllamaAPI + "/api/tags")
	if err != nil {
		return preflightCheck{"Model availability", symbolFail, "cannot check — Ollama unreachable", true}
	}
	defer resp.Body.Close()
	var payload struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return preflightCheck{"Model availability", symbolWarn, "could not parse model list", false}
	}
	for _, m := range payload.Models {
		if strings.HasPrefix(m.Name, config.ModelName) {
			return preflightCheck{"Model availability", symbolOK, m.Name, false}
		}
	}
	names := make([]string, 0, len(payload.Models))
	for _, m := range payload.Models {
		names = append(names, m.Name)
	}
	hint := strings.Join(names, ", ")
	if hint == "" {
		hint = "(none)"
	}
	return preflightCheck{"Model availability", symbolFail,
		fmt.Sprintf("%q not pulled  →  ollama pull %s  (have: %s)", config.ModelName, config.ModelName, hint), true}
}

// ── Project context ───────────────────────────────────────────────────────────

// contextBudget caps total bytes injected as project context per prompt.
// qwen2.5-coder:7b produces empty responses when the prompt is too large.
// 3000 bytes ≈ 750 tokens, leaving plenty of room for system prompt + reply.
const contextBudget = 3000

// treeLineLimit caps tree output lines to avoid overwhelming small models.
const treeLineLimit = 40

func buildProjectContext() string {
	var b strings.Builder
	b.WriteString("### PROJECT TREE\n")

	tree := getProjectTree()
	tree = strings.ReplaceAll(tree, config.WorkspaceDir, "/workspace/")
	b.WriteString(tree)

	if remaining := contextBudget - b.Len(); remaining > 200 {
		if kf := readKeyFiles(remaining); kf != "" {
			b.WriteString("\n### KEY FILE CONTENTS\n")
			b.WriteString(kf)
		}
	}
	return b.String()
}

func getProjectTree() string {
	out, err := exec.Command(
		"tree", "-L", config.TreeDepth,
		"-I", "node_modules|.git|.next|dist|build|__pycache__|*.pyc|.turbo|bin|vendor",
		"--dirsfirst", config.WorkspaceDir,
	).Output()

	var raw string
	if err != nil {
		fallback, ferr := exec.Command("ls", "-1", config.WorkspaceDir).Output()
		if ferr != nil {
			return "(could not read project — install tree: sudo apt install tree)\n"
		}
		raw = string(fallback)
	} else {
		raw = string(out)
	}

	lines := strings.Split(raw, "\n")
	if len(lines) > treeLineLimit {
		extra := len(lines) - treeLineLimit
		lines = lines[:treeLineLimit]
		lines = append(lines, fmt.Sprintf("… (%d more lines — type 'tree' to see full structure)", extra))
	}
	return strings.Join(lines, "\n") + "\n"
}

func readKeyFiles(budget int) string {
	var b strings.Builder
	for _, name := range strings.Split(config.KeyFiles, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(config.WorkspaceDir, name))
		if err != nil {
			continue
		}
		content := strings.TrimSpace(string(data))
		if content == "" {
			continue
		}
		perFileCap := budget / 2
		if perFileCap > 1024 {
			perFileCap = 1024
		}
		if len(content) > perFileCap {
			content = content[:perFileCap] + "\n… (truncated)"
		}
		entry := fmt.Sprintf("\n// %s\n```\n%s\n```\n", name, content)
		if b.Len()+len(entry) > budget {
			break
		}
		b.WriteString(entry)
	}
	return b.String()
}

// ── Shell block extractor ─────────────────────────────────────────────────────

func extractShellBlocks(aiResponse string) []string {
	re := regexp.MustCompile(`(?s)` + "```" + `(?:bash|sh)[\t ]*\n(.*?)` + "```" + `[\t ]*`)
	matches := re.FindAllStringSubmatch(aiResponse, -1)

	blocks := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		block := strings.TrimSpace(match[1])
		if block == "" {
			continue
		}
		lc := strings.ToLower(block)
		known :=
			strings.Contains(lc, "cat <<") || strings.Contains(lc, "mkdir") ||
				strings.Contains(lc, "touch") || strings.Contains(lc, "cp ") ||
				strings.Contains(lc, "mv ") || strings.Contains(lc, "rm ") ||
				strings.Contains(lc, "echo ") || strings.Contains(lc, "chmod") ||
				strings.Contains(lc, "go build") || strings.Contains(lc, "go run") ||
				strings.Contains(lc, "go mod") || strings.Contains(lc, "go get") ||
				strings.Contains(lc, "npm ") || strings.Contains(lc, "npx ") ||
				strings.Contains(lc, "pnpm ") || strings.Contains(lc, "yarn ") ||
				strings.Contains(lc, "docker ") || strings.Contains(lc, "vercel ") ||
				strings.Contains(lc, "git ") || strings.Contains(lc, "curl ") ||
				strings.Contains(lc, "wget ") || strings.Contains(lc, "ls ") ||
				strings.Contains(lc, "pwd") || strings.Contains(lc, "cd ")
		if known {
			blocks = append(blocks, block)
		} else {
			fmt.Printf("%s  Skipping unrecognised shell block:\n%s\n", symbolWarn, block)
		}
	}
	return blocks
}
