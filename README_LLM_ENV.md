# LLM/CI/Container Test Environment Note

If you are running Go tests or builds in a container, CI, or restricted environment (such as Podman, Docker, or a cloud shell) and encounter errors like:

    fork/exec /tmp/go-build.../testbinary: permission denied

This is due to the environment blocking execution from `/tmp` (common in rootless containers or with certain security policies).

## Solution: Use a user-writable temp directory

Set the following environment variables before running `go test` or any Go build that executes binaries:

```bash
export GOCACHE=$HOME/.cache/go-build
export GOTMPDIR=$HOME/.tmp
mkdir -p $GOCACHE $GOTMPDIR
```

Then run your tests as usual:

```bash
go test ./...
```

This will ensure all build/test artifacts are created in directories you own and can execute from.

---

**Why?**
- Go by default uses `/tmp` for temporary build/test files. Some environments mount `/tmp` with `noexec` or restrict execution for security.
- Setting `GOTMPDIR` and `GOCACHE` to a user-writable, executable directory avoids this issue.

**Tip:**
- You can add these exports to your `.bashrc`, `.zshrc`, or CI pipeline setup scripts for convenience.

---

_This file was added to help LLM agents and developers avoid common container/CI test execution issues._
