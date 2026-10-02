// Package status provides pre-flight diagnostics for JAVE.
// It checks every external dependency before the main loop starts
// and prints a clear status dashboard so the user always knows what
// is working, what is broken, and exactly how to fix it.
package status

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"jave/internal/config"
)

// ── ANSI colours ──────────────────────────────────────────────────────────────

const (
	colReset  = "\033[0m"
	colGreen  = "\033[32m"
	colYellow = "\033[33m"
	colRed    = "\033[31m"
	colCyan   = "\033[36m"
	colBold   = "\033[1m"
)

func ok(label, detail string) {
	fmt.Printf("  %s✓%s  %-22s %s%s%s\n", colGreen, colReset, label, colCyan, detail, colReset)
}

func warn(label, detail, fix string) {
	fmt.Printf("  %s⚠%s  %-22s %s%s%s\n", colYellow, colReset, label, colYellow, detail, colReset)
	if fix != "" {
		fmt.Printf("       %s↳ %s%s\n", colYellow, fix, colReset)
	}
}

func fail(label, detail, fix string) {
	fmt.Printf("  %s✗%s  %-22s %s%s%s\n", colRed, colReset, label, colRed, detail, colReset)
	if fix != "" {
		fmt.Printf("       %s↳ %s%s\n", colRed, fix, colReset)
	}
}

// CheckResult summarises all pre-flight checks.
type CheckResult struct {
	OllamaRunning bool
	ModelReady    bool
	SandboxReady  bool
	WorkspaceOK   bool
	TreeInstalled bool
	// FatalErrors is true when JAVE cannot function at all.
	FatalErrors bool
	// AvailableModels lists whatever is pulled in Ollama.
	AvailableModels []string
}

// RunAll executes every check, prints the dashboard, and returns the summary.
// Call this once at startup before the main prompt loop.
func RunAll() CheckResult {
	fmt.Printf("\n%s%s━━━  JAVE PRE-FLIGHT  ━━━%s\n\n", colBold, colCyan, colReset)

	result := CheckResult{}

	checkOllama(&result)
	checkModel(&result)
	checkSandbox(&result)
	checkWorkspace(&result)
	checkTree(&result)

	fmt.Println()

	result.FatalErrors = !result.OllamaRunning || !result.ModelReady

	if result.FatalErrors {
		fmt.Printf("%s%s  JAVE cannot start — fix the errors above first.%s\n\n",
			colBold, colRed, colReset)
	} else if !result.SandboxReady {
		fmt.Printf("%s%s  JAVE starting in READ-ONLY mode — sandbox unavailable.%s\n",
			colBold, colYellow, colReset)
		fmt.Printf("  %sShell blocks will be shown but NOT executed.%s\n\n",
			colYellow, colReset)
	} else {
		fmt.Printf("%s%s  All systems go. JAVE is ready.%s\n\n", colBold, colGreen, colReset)
	}

	fmt.Printf("%s━━━━━━━━━━━━━━━━━━━━━━━━━━━━%s\n\n", colCyan, colReset)

	return result
}

// ── Individual checks ─────────────────────────────────────────────────────────

func checkOllama(result *CheckResult) {
	client := &http.Client{Timeout: 4 * time.Second}
	resp, err := client.Get(config.OllamaAPI + "/api/tags")
	if err != nil {
		fail("Ollama", "not reachable at "+config.OllamaAPI,
			"Run:  ollama serve   (in a separate terminal)")
		return
	}
	defer resp.Body.Close()
	result.OllamaRunning = true

	// Parse the model list while we have the response open.
	var tagsResp struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tagsResp); err == nil {
		for _, m := range tagsResp.Models {
			result.AvailableModels = append(result.AvailableModels, m.Name)
		}
	}

	if len(result.AvailableModels) == 0 {
		ok("Ollama", "running  (no models pulled yet)")
	} else {
		ok("Ollama", "running  ("+strings.Join(result.AvailableModels, ", ")+")")
	}
}

func checkModel(result *CheckResult) {
	if !result.OllamaRunning {
		fail("Model: "+config.ModelName, "skipped — Ollama not running", "")
		return
	}

	wantedBase := strings.TrimSuffix(config.ModelName, ":latest")
	for _, name := range result.AvailableModels {
		nameBase := strings.TrimSuffix(name, ":latest")
		if nameBase == wantedBase || name == config.ModelName {
			result.ModelReady = true
			ok("Model", config.ModelName+" ready")
			return
		}
	}

	// Model not found — build a helpful hint.
	hint := "Run:  ollama pull " + config.ModelName
	if len(result.AvailableModels) > 0 {
		hint += "\n       Available: " + strings.Join(result.AvailableModels, ", ")
	}
	fail("Model: "+config.ModelName, "not pulled", hint)
}

func checkSandbox(result *CheckResult) {
	// First check Docker itself.
	dockerInfo := exec.Command("docker", "info")
	if err := dockerInfo.Run(); err != nil {
		fail("Docker", "not running",
			"Run:  sudo systemctl start docker")
		return
	}

	// Check whether the sandbox container is up.
	inspectCmd := exec.Command("docker", "inspect",
		"--format", "{{.State.Status}}",
		config.SandboxContainer,
	)
	out, err := inspectCmd.Output()
	if err != nil {
		warn("Sandbox", config.SandboxContainer+" container not found",
			"Run:  make sandbox-start")
		return
	}

	containerStatus := strings.TrimSpace(string(out))
	if containerStatus != "running" {
		warn("Sandbox", config.SandboxContainer+" is "+containerStatus,
			"Run:  make sandbox-restart")
		return
	}

	// Check that /workspace is actually mounted and writable.
	testCmd := exec.Command("docker", "exec", config.SandboxContainer,
		"bash", "-c", "touch /workspace/.jave_probe && rm /workspace/.jave_probe")
	if err := testCmd.Run(); err != nil {
		warn("Sandbox mount", "/workspace not writable inside container",
			"Run:  make sandbox-restart   (workspace may not be mounted)")
		return
	}

	result.SandboxReady = true
	ok("Sandbox", config.SandboxContainer+" running · /workspace writable")
}

func checkWorkspace(result *CheckResult) {
	info, err := os.Stat(config.WorkspaceDir)
	if err != nil {
		fail("Workspace", config.WorkspaceDir+" not found",
			"Run:  mkdir -p "+config.WorkspaceDir+
				"  or  export JAVE_WORKSPACE=/your/project")
		return
	}
	if !info.IsDir() {
		fail("Workspace", config.WorkspaceDir+" is not a directory",
			"Set JAVE_WORKSPACE to a valid directory path")
		return
	}

	// Test write access.
	probe := config.WorkspaceDir + "/.jave_probe"
	if err := os.WriteFile(probe, []byte("ok"), 0644); err != nil {
		warn("Workspace", config.WorkspaceDir+" exists but is not writable",
			"Run:  chmod u+w "+config.WorkspaceDir)
		_ = os.Remove(probe)
		return
	}
	_ = os.Remove(probe)

	result.WorkspaceOK = true
	ok("Workspace", config.WorkspaceDir)
}

func checkTree(result *CheckResult) {
	if _, err := exec.LookPath("tree"); err != nil {
		warn("tree binary", "not installed — falling back to ls",
			"Run:  sudo apt install tree")
		return
	}
	result.TreeInstalled = true
	ok("tree", "installed")
}

// ── Runtime error reporters ───────────────────────────────────────────────────
// These are called during execution, not just at startup.

// StreamError prints a clear message when the Ollama stream fails mid-response.
func StreamError(err error) {
	fmt.Printf("\n%s%s[JAVE STREAM ERROR]%s\n", colBold, colRed, colReset)
	fmt.Printf("  %s%v%s\n", colRed, err, colReset)
	fmt.Printf("  %s↳ Possible causes:%s\n", colYellow, colReset)
	fmt.Println("     • Ollama was killed or crashed mid-generation")
	fmt.Println("     • The model ran out of memory (try a shorter prompt)")
	fmt.Println("     • Context window exceeded (type 'reset' to clear history)")
	fmt.Printf("  %s↳ Fix: check  ollama ps  in another terminal%s\n\n", colYellow, colReset)
}

// SandboxError prints a structured message when a shell block fails to execute.
func SandboxError(command string, err error) {
	fmt.Printf("\n%s%s[JAVE SANDBOX ERROR]%s\n", colBold, colRed, colReset)
	fmt.Printf("  %sCommand failed:%s %s\n", colRed, colReset, err)
	// Show only the first line of the command to keep output readable.
	firstLine := strings.SplitN(strings.TrimSpace(command), "\n", 2)[0]
	fmt.Printf("  %sFirst line:    %s %s\n", colYellow, colReset, firstLine)
	fmt.Printf("  %s↳ Run  make sandbox-shell  to inspect the container manually%s\n\n",
		colYellow, colReset)
}

// BlockSkipped notifies the user when a shell block was not recognised
// and therefore skipped — so they know it wasn't silently lost.
func BlockSkipped(block string) {
	firstLine := strings.SplitN(strings.TrimSpace(block), "\n", 2)[0]
	fmt.Printf("  %s⚠  Shell block skipped (no recognised command):%s %s\n",
		colYellow, colReset, firstLine)
}

// OllamaStuck prints a message when the model stops producing tokens
// but hasn't signalled done — e.g. a hang or a very slow generation.
func OllamaStuck(elapsed time.Duration) {
	fmt.Printf("\n  %s⏳ JAVE has been thinking for %s ...%s\n",
		colYellow, elapsed.Round(time.Second), colReset)
	fmt.Printf("  %s   Press Ctrl+C to cancel and try a shorter prompt.%s\n\n",
		colYellow, colReset)
}