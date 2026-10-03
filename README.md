# strictgo

`strictgo` gives coding agents a deterministic JSON feedback loop for ordinary Go code. It checks formatting, compilation, `go vet`, and a small strict coding profile. Its analyzer respects build constraints and uses Go type information to find ignored errors.

## Install

```sh
go install github.com/ilvar/strictgo@latest
strictgo install-skills
```

The skill installer detects Codex and Claude Code and writes `SKILL.md` to their user skill directories. It is idempotent and refuses to replace a changed copy. Building strictgo requires Go 1.26 or newer.

## Use

```sh
strictgo check .
strictgo fix .
strictgo thorough .
strictgo new myapp
strictgo --help
```

`check`, `fix`, and `thorough` accept a path to a directory containing `go.mod`. A bare path is an alias for `check`. Operational commands write one JSON document to stdout and return 0 when clean, 1 for remaining diagnostics, or 2 for invocation and operational errors. Help prints plain text.

A report contains `ok`, `error_count`, `warning_count`, and a deterministic `diagnostics` array. Each diagnostic has a `level`, `source`, `code`, `message`, and optional `at` location with start and end positions and source snippet. Safe fixes include replacement text and byte offsets. The `fix` command applies `gofmt` and typed dot-import suggestions, validates edits within the project root, and re-checks. It rolls back a suggested fix if it introduces compiler diagnostics.

## Strict profile

| Code | Rule |
| --- | --- |
| `strictgo::format` | Use `gofmt`. |
| `strictgo::no_panic` | Return errors instead of panicking outside tests. |
| `strictgo::no_dot_import` | Name imported packages explicitly. |
| `strictgo::no_mutable_global` | Pass state explicitly instead of using package variables outside tests. |
| `strictgo::must_handle` | Handle error-returning calls or explicitly discard the result. |
| `strictgo::capability_boundary` | Put direct filesystem, network, and process access in a package named `capability`. |

The capability package lives in a directory named `capability`, commonly `internal/capability`. The checker identifies standard library package operations and methods on known resource types. It is a focused static check, not a whole-program effect analysis.

`check` uses `go test -run '^$' ./...` to compile packages and tests without running ordinary test functions; custom `TestMain` functions may still run.

## Thorough gate

`strictgo thorough .` runs the normal check, then `go test ./...`, `go test -race ./...`, and each discovered fuzz target for two seconds with one fuzz worker. It reports failures in the same JSON schema. Run it when the additional time and race detector support are available. Go fuzzing stores failing inputs in `testdata/fuzz/`; retain those files as regression cases.

## Development

```sh
go test ./...
go vet ./...
go run . check .
go run . thorough .
```
