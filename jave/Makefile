.PHONY: all build tidy test run deps sys-deps setup clean docker-check \
        sandbox-start sandbox-stop sandbox-shell sandbox-logs sandbox-restart \
        sandbox-ls sandbox-install-go

# ── Paths ─────────────────────────────────────────────────────────────────────
# Always export JAVE_WORKSPACE before running:
#   export JAVE_WORKSPACE=/home/deadcode/Documents/my-project && make run

WORKSPACE ?= $(JAVE_WORKSPACE)
ifeq ($(strip $(WORKSPACE)),)
  WORKSPACE = /mnt/workshop/jave_engine/jave
endif

# golang:1.22-bullseye — has Go built in AND we install Node on top.
# This means the sandbox can run BOTH go build AND npm/npx commands.
SANDBOX_IMAGE = golang:1.22-bullseye
SANDBOX_NAME  = jave-sandbox

# ── Top-level ─────────────────────────────────────────────────────────────────

all: deps build

build:
	mkdir -p bin
	go build -o bin/jave ./cmd/jave

tidy:
	go mod tidy

deps:
	go mod download

test:
	go test ./...

# make run:
#   1. Checks Docker is up
#   2. Ensures sandbox is running with the correct WORKSPACE mount
#   3. Builds the JAVE binary
#   4. Launches JAVE (pre-flight banner runs inside)
run: docker-check sandbox-start build
	./bin/jave

# ── Pre-flight ────────────────────────────────────────────────────────────────

docker-check:
	@docker info > /dev/null 2>&1 || \
	  (echo ""; \
	   echo "❌  Docker is not running."; \
	   echo "    Fix: sudo systemctl start docker"; \
	   echo ""; \
	   exit 1)

# ── Setup ─────────────────────────────────────────────────────────────────────

setup: sys-deps sandbox-start
	@echo ""
	@echo "✅  Setup complete."
	@echo "    Pull the model : ollama pull qwen2.5-coder:7b"
	@echo "    Start JAVE     : export JAVE_WORKSPACE=/your/project && make run"

sys-deps:
	sudo apt update
	sudo apt install -y build-essential git pkg-config tree wget curl nodejs npm

# ── Docker sandbox ────────────────────────────────────────────────────────────
#
# Image: golang:1.22-bullseye
#   - Go 1.22 is pre-installed  →  go build / go run work out of the box
#   - We install Node 20 + npm tools on top  →  npm / npx / vercel also work
#
# sandbox-start detects mount mismatches and restarts automatically.

sandbox-start: docker-check
	@RUNNING=$$(docker inspect --format='{{.State.Running}}' $(SANDBOX_NAME) 2>/dev/null); \
	CURRENT_MOUNT=$$(docker inspect --format='{{range .Mounts}}{{.Source}}{{end}}' $(SANDBOX_NAME) 2>/dev/null); \
	WANT_MOUNT="$(WORKSPACE)"; \
	if [ "$$RUNNING" = "true" ] && [ "$$CURRENT_MOUNT" = "$$WANT_MOUNT" ]; then \
	  echo "  ✅  Sandbox '$(SANDBOX_NAME)' running  →  $$CURRENT_MOUNT:/workspace"; \
	elif [ "$$RUNNING" = "true" ] && [ "$$CURRENT_MOUNT" != "$$WANT_MOUNT" ]; then \
	  echo "  ⚠️   Mount mismatch:"; \
	  echo "       running : $$CURRENT_MOUNT"; \
	  echo "       wanted  : $$WANT_MOUNT"; \
	  echo "  🔄  Restarting with correct mount..."; \
	  docker stop $(SANDBOX_NAME) > /dev/null 2>&1 || true; \
	  docker rm   $(SANDBOX_NAME) > /dev/null 2>&1 || true; \
	  $(MAKE) _sandbox_create WORKSPACE="$$WANT_MOUNT"; \
	else \
	  echo "  ⏳  Starting sandbox (mounting: $(WORKSPACE):/workspace)..."; \
	  $(MAKE) _sandbox_create WORKSPACE="$(WORKSPACE)"; \
	fi

_sandbox_create:
	@docker run -d \
	  --name $(SANDBOX_NAME) \
	  -v "$(WORKSPACE):/workspace" \
	  -w /workspace \
	  -p 3000:3000 \
	  -p 8080:8080 \
	  $(SANDBOX_IMAGE) \
	  tail -f /dev/null > /dev/null
	@echo "  ⏳  Installing Node + global tools..."
	@docker exec $(SANDBOX_NAME) bash -c \
	  "curl -fsSL https://deb.nodesource.com/setup_20.x | bash - > /dev/null 2>&1 \
	   && apt-get install -y nodejs > /dev/null 2>&1"
	@docker exec $(SANDBOX_NAME) npm install -g \
	  vercel typescript eslint prettier > /dev/null 2>&1
	@docker exec $(SANDBOX_NAME) go version
	@docker exec $(SANDBOX_NAME) node --version
	@echo "  ✅  Sandbox ready  →  $(WORKSPACE):/workspace"

sandbox-stop:
	@docker stop $(SANDBOX_NAME) 2>/dev/null || true
	@docker rm   $(SANDBOX_NAME) 2>/dev/null || true
	@echo "  🛑  Sandbox stopped."

sandbox-restart: sandbox-stop
	@$(MAKE) _sandbox_create WORKSPACE="$(WORKSPACE)"

sandbox-shell:
	docker exec -it $(SANDBOX_NAME) bash

sandbox-logs:
	docker logs -f $(SANDBOX_NAME)

sandbox-ls:
	@echo "  📁  /workspace inside '$(SANDBOX_NAME)':"
	@docker exec $(SANDBOX_NAME) ls -la /workspace

# ── Cleanup ───────────────────────────────────────────────────────────────────

clean:
	rm -rf bin/