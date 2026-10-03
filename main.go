// strictgo: capability
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const help = `strictgo — strict Go feedback loop for coding agents

PURPOSE
  strictgo keeps ordinary Go syntax and semantics, then adds a strict coding
  profile and a deterministic check → fix → re-check interface.

INSTALL
  go install github.com/ilvar/strictgo@latest
  strictgo install-skills

COMMANDS
  strictgo --help                 Print this manual (-h and command --help work too).
  strictgo check [PATH]           Check a Go module; PATH defaults to .
  strictgo fix [PATH]             Apply gofmt edits and re-check.
  strictgo new <NAME>             Create a Go project in the current directory.
  strictgo install-skills         Install the bundled Codex/Claude Code skill.
  strictgo [PATH]                 Alias for strictgo check PATH.

AGENT WORKFLOW
  Inspect the project, make a small coherent edit, run strictgo check PATH,
  address every error in its JSON report, then run go test ./... and inspect
  the final diff. Use strictgo fix PATH for deterministic gofmt edits.

OUTPUT CONTRACT
  Operational commands write one JSON document to stdout. Help is plain text.
  Reports contain ok, error_count, warning_count, and diagnostics. Diagnostics
  are sorted by file, line, column, code, and message. An optional at field
  gives file, positions, and source snippet. Operational failures also return
  a JSON report. Human details may be written to stderr.

EXIT STATUS
  0  help printed, project created, skills installed, or report clean
  1  check/fix completed with error diagnostics
  2  invalid invocation or operational failure

STRICT SUBSET
  strictgo::format               Source must be gofmt formatted.
  strictgo::no_panic             Avoid panic outside tests; return errors.
  strictgo::no_dot_import        Name imported packages explicitly.
  strictgo::no_mutable_global    Avoid package-level variables outside tests.
  strictgo::must_handle          Handle results of known error-returning calls.
  strictgo::capability_boundary  Keep filesystem, network, and process access
                                 in a file marked // strictgo: capability
                                 immediately before its package declaration.

CHECKS AND FIXES
  strictgo combines gofmt, go vet, a compile-only go test run, and the strict
  subset above. It does not run the test bodies. Its fix command applies only
  deterministic gofmt output; other diagnostics need a source edit. Go tool
  diagnostics are reported without invented replacement text.

SKILL INSTALLATION
  Installs SKILL.md to ~/.agents/skills/strictgo and/or
  ~/.claude/skills/strictgo when the corresponding client is detected. Existing
  modified files are never overwritten.
`

type Position struct {
	Line   int `json:"line"`
	Column int `json:"column"`
}
type Location struct {
	File    string   `json:"file"`
	Start   Position `json:"start"`
	End     Position `json:"end"`
	Snippet string   `json:"snippet"`
}
type Diagnostic struct {
	Level   string    `json:"level"`
	Source  string    `json:"source"`
	Code    string    `json:"code"`
	Message string    `json:"message"`
	At      *Location `json:"at,omitempty"`
}
type Report struct {
	OK           bool         `json:"ok"`
	ErrorCount   int          `json:"error_count"`
	WarningCount int          `json:"warning_count"`
	Diagnostics  []Diagnostic `json:"diagnostics"`
}

func main() { os.Exit(run(os.Args[1:])) }
func run(args []string) int {
	if len(args) == 0 {
		args = []string{"check", "."}
	}
	for _, a := range args {
		if a == "--help" || a == "-h" || a == "help" {
			fmt.Print(help)
			return 0
		}
	}
	command := args[0]
	switch command {
	case "check", "fix":
		if len(args) > 2 {
			return failure("usage: strictgo " + command + " [PATH]")
		}
		path := "."
		if len(args) == 2 {
			path = args[1]
		}
		r, err := check(path, command == "fix")
		if err != nil {
			return failure(err.Error())
		}
		emit(r)
		if r.OK {
			return 0
		}
		return 1
	case "new":
		if len(args) != 2 {
			return failure("usage: strictgo new <NAME>")
		}
		if err := createProject(args[1]); err != nil {
			return failure(err.Error())
		}
		emit(Report{OK: true, Diagnostics: []Diagnostic{}})
		return 0
	case "install-skills":
		if len(args) != 1 {
			return failure("usage: strictgo install-skills")
		}
		if err := installSkills(); err != nil {
			return failure(err.Error())
		}
		emit(Report{OK: true, Diagnostics: []Diagnostic{}})
		return 0
	default:
		if len(args) == 1 && !strings.HasPrefix(command, "-") {
			r, err := check(command, false)
			if err != nil {
				return failure(err.Error())
			}
			emit(r)
			if r.OK {
				return 0
			}
			return 1
		}
		return failure("invalid invocation; see strictgo --help")
	}
}
func emit(r Report) {
	if r.Diagnostics == nil {
		r.Diagnostics = []Diagnostic{}
	}
	_ = json.NewEncoder(os.Stdout).Encode(r)
}
func failure(message string) int {
	emit(Report{Diagnostics: []Diagnostic{{Level: "error", Source: "strictgo", Code: "strictgo::operation", Message: message}}})
	_, _ = fmt.Fprintln(os.Stderr, message)
	return 2
}
func moduleRoot(path string) (string, error) {
	root, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(root)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("project path is not a directory")
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	if _, err = os.Stat(filepath.Join(root, "go.mod")); err != nil {
		return "", fmt.Errorf("no go.mod in %s", root)
	}
	return root, nil
}
