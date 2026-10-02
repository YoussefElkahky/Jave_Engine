package config

import "os"

// getEnv returns the value of the environment variable key,
// or fallback when the variable is unset or empty.
func getEnv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

// WorkspaceDir is the root directory Jave scans and builds inside.
// Override with JAVE_WORKSPACE when working on a different project.
var WorkspaceDir = getEnv("JAVE_WORKSPACE", "/mnt/workshop/jave_engine/jave/")

// Ollama connection and model selection.
var (
	OllamaAPI = getEnv("JAVE_OLLAMA_API", "http://127.0.0.1:11434")

	// qwen2.5-coder:7b — purpose-built coding model, runs well on 16 GB RAM.
	// Switch to qwen2.5-coder:14b if you ever get more RAM.
	ModelName = getEnv("JAVE_MODEL", "qwen2.5-coder:7b")
)

// SandboxContainer is the Docker container Jave executes shell blocks inside.
var SandboxContainer = getEnv("JAVE_SANDBOX_CONTAINER", "jave-sandbox")

// TreeDepth controls how many directory levels are included in the project
// context sent to the model. 4 is a good balance of detail vs token cost.
var TreeDepth = getEnv("JAVE_TREE_DEPTH", "4")

// KeyFiles is a comma-separated list of filenames that Jave will read and
// inject into the system prompt so the model sees real config content.
// Override with JAVE_KEY_FILES to add project-specific files.
var KeyFiles = getEnv("JAVE_KEY_FILES",
	"package.json,tsconfig.json,tailwind.config.ts,tailwind.config.js,next.config.ts,next.config.js,go.mod",
)