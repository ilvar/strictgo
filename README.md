# strictgo

`strictgo` gives coding agents a deterministic JSON feedback loop for ordinary Go code. It checks formatting, compilation, `go vet`, and a small strict coding profile. `fix` applies `gofmt` only; other diagnostics require a deliberate source change.

## Install

```sh
go install github.com/ilvar/strictgo@latest
strictgo install-skills
```

The skill installer detects Codex and Claude Code and writes `SKILL.md` to their user skill directories. It is idempotent and refuses to replace a changed copy.

## Use

```sh
strictgo check .
strictgo fix .
strictgo new myapp
strictgo --help
```

`check` and `fix` accept a path to a directory containing `go.mod`. A bare path is an alias for `check`. Operational commands write one JSON document to stdout and return 0 when clean, 1 for remaining diagnostics, or 2 for invocation and operational errors. Help prints plain text.

A report contains `ok`, `error_count`, `warning_count`, and a deterministic `diagnostics` array. Each diagnostic has a `level`, `source`, `code`, `message`, and optional `at` location with start and end positions and source snippet. Go compiler and vet findings do not carry invented fixes.

## Strict profile

| Code | Rule |
| --- | --- |
| `strictgo::format` | Use `gofmt`. |
| `strictgo::no_panic` | Return errors instead of panicking outside tests. |
| `strictgo::no_dot_import` | Name imported packages explicitly. |
| `strictgo::no_mutable_global` | Pass state explicitly instead of using package variables outside tests. |
| `strictgo::must_handle` | Handle results from recognized error-returning standard library calls. |
| `strictgo::capability_boundary` | Put filesystem, network, and process access in a capability file. |

Mark a capability file by placing this exact comment immediately before `package`:

```go
// strictgo: capability
package filesystem
```

The checker recognizes direct calls to standard library packages and a set of common ignored-error calls. It is a focused static check, not a whole-program effect or type analysis. `go test -run '^$' ./...` compiles packages and tests without running ordinary test functions; custom `TestMain` functions may still run. Run the full test suite separately.

## Development

```sh
go test ./...
go vet ./...
strictgo check .
```
