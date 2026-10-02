# TODO

- [ ] Plan approval: workspace path fixes + Ctrl+C cleanup
- [ ] Update internal/sandbox/exec.go to strip host workspace absolute prefix and run docker exec with /workspace-relative paths
- [ ] Update Makefile to mount JAVE_WORKSPACE dynamically so it is visible at /workspace inside sandbox
- [ ] Update cmd/jave/main.go system prompt (and/or buildProjectContext) to instruct model to use /workspace/ paths
- [ ] Add behavior: when user presses Ctrl+C in JAVE, run sandbox-stop and then exit
- [ ] Build + quick smoke test: make sandbox-start, make run

