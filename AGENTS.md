# Agent Crucible

A deterministic fault-injection harness for tool-using agents, written in pure Go. See `README.md` for the product overview and `Makefile` for the canonical commands.

## Cursor Cloud specific instructions

- Pure Go module (`go 1.22`) with **no external dependencies** (`go.mod` has no `require` block), so the toolchain shipped in the image is all that's needed. The update script (`go mod download`) is effectively a no-op today but stays correct if deps are added later.
- The `web/` directory is served via Go's `embed` (`web/embed.go`), so the frontend is compiled into the `crucible` binary — there is no separate JS build/install step. Editing files under `web/` requires rebuilding the binary to see changes.
- Standard commands (all defined in `Makefile`): `make vet` (lint), `make test`, `make build`, `make serve` (builds + serves on `127.0.0.1:8080`), `make run`, `make replay`.
- `make test` is three suites: `go test ./...`, the Python sidecar smoke (`PYTHONPATH=runtime python3 -m crucible_rt smoke`, needs `make runtime` first), and the Node sidecar self-test (`node runtime/js/selftest.mjs`). The Go tests that need a sidecar skip themselves when Python or Node is missing, so a green `go test` alone does not mean the drop-in paths were exercised.
- Run the server directly with `./crucible serve` after `go build -o crucible ./cmd/crucible`. It binds `127.0.0.1:8080` by default and warns on a non-loopback `-addr`: the run API imports agent files from the checkout and has no authentication. Health check is `GET /api/health`. Core endpoints: `POST /api/run`, `POST /api/sweep`, `POST /api/replay`, `GET /api/meta`.
- The runner is deterministic: the same `seed`/`trial`/`p`/`faults` replay bit-for-bit, so tests and manual runs are reproducible. Fault decisions come from per-site RNG sub-streams, so raising `p` on a fixed seed only adds faults — if a change makes an existing fault disappear or move, that is a regression, and `TestDecisionsAreStableAsPRises` is the check.
- `TestDemoSeed42Locked` pins the demo suite's survival and counts. It exists to make behavioural changes visible; if you change fault injection or scoring, re-pin it deliberately rather than adjusting it to whatever the code now prints.
