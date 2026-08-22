# Agent Crucible

A deterministic fault-injection harness for tool-using agents, written in pure Go. See `README.md` for the product overview and `Makefile` for the canonical commands.

## Cursor Cloud specific instructions

- Pure Go module (`go 1.22`) with **no external dependencies** (`go.mod` has no `require` block), so the toolchain shipped in the image is all that's needed. The update script (`go mod download`) is effectively a no-op today but stays correct if deps are added later.
- The `web/` directory is served via Go's `embed` (`web/embed.go`), so the frontend is compiled into the `crucible` binary — there is no separate JS build/install step. Editing files under `web/` requires rebuilding the binary to see changes.
- Standard commands (all defined in `Makefile`): `make vet` (lint), `make test` (`go test ./...`), `make build`, `make serve` (builds + serves on `:8080`), `make run`, `make replay`.
- Run the server directly with `./crucible serve -addr :8080` after `go build -o crucible ./cmd/crucible`. It listens on `:8080`; health check is `GET /api/health`. Core endpoints: `POST /api/run`, `POST /api/sweep`, `POST /api/replay`, `GET /api/meta`.
- The runner is deterministic: the same `seed`/`trial`/`p`/`faults` replay bit-for-bit, so tests and manual runs are reproducible.
