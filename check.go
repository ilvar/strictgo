// strictgo: capability
package main

import (
	"bufio"
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

func check(path string, fix bool) (Report, error) {
	root, err := moduleRoot(path)
	if err != nil {
		return Report{}, err
	}
	report := Report{Diagnostics: []Diagnostic{}}
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != root && (strings.HasPrefix(entry.Name(), ".") || entry.Name() == "vendor" || entry.Name() == "testdata") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".go") {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		formatted, fmtErr := format.Source(data)
		if fmtErr == nil && !bytes.Equal(data, formatted) {
			if fix {
				if err := os.WriteFile(path, formatted, entryMode(path)); err != nil {
					return err
				}
				data = formatted
			} else {
				report.Diagnostics = append(report.Diagnostics, Diagnostic{Level: "error", Source: "strictgo", Code: "strictgo::format", Message: "file is not gofmt formatted; run strictgo fix", At: &Location{File: rel, Start: Position{Line: 1, Column: 1}, End: Position{Line: 1, Column: 1}}})
			}
		}
		report.Diagnostics = append(report.Diagnostics, scanFile(rel, data)...)
		return nil
	})
	if err != nil {
		return Report{}, err
	}
	report.Diagnostics = append(report.Diagnostics, runGoTool(root, "test", "-run", "^$", "./...")...)
	report.Diagnostics = append(report.Diagnostics, runGoTool(root, "vet", "./...")...)
	sort.Slice(report.Diagnostics, func(i, j int) bool {
		a, b := report.Diagnostics[i], report.Diagnostics[j]
		af, bf := "", ""
		al, bl, ac, bc := 0, 0, 0, 0
		if a.At != nil {
			af = a.At.File
			al = a.At.Start.Line
			ac = a.At.Start.Column
		}
		if b.At != nil {
			bf = b.At.File
			bl = b.At.Start.Line
			bc = b.At.Start.Column
		}
		if af != bf {
			return af < bf
		}
		if al != bl {
			return al < bl
		}
		if ac != bc {
			return ac < bc
		}
		if a.Code != b.Code {
			return a.Code < b.Code
		}
		return a.Message < b.Message
	})
	for _, d := range report.Diagnostics {
		if d.Level == "error" {
			report.ErrorCount++
		} else if d.Level == "warning" {
			report.WarningCount++
		}
	}
	report.OK = report.ErrorCount == 0
	return report, nil
}
func entryMode(path string) os.FileMode {
	info, err := os.Stat(path)
	if err != nil {
		return 0644
	}
	return info.Mode().Perm()
}
func scanFile(rel string, data []byte) []Diagnostic {
	knownErrorCalls := map[string]bool{
		"os.Remove": true, "os.RemoveAll": true, "os.Mkdir": true, "os.MkdirAll": true, "os.WriteFile": true, "os.Rename": true, "os.Chdir": true, "os.Setenv": true, "os.Unsetenv": true,
		"fmt.Fprint": true, "fmt.Fprintln": true, "fmt.Fprintf": true, "fmt.Scan": true, "fmt.Scanf": true, "fmt.Scanln": true, "fmt.Fscan": true, "fmt.Fscanf": true, "fmt.Fscanln": true,
		"io.Copy": true, "io.CopyN": true, "io.ReadAll": true, "encoding/json.Unmarshal": true, "json.NewEncoder.Encode": true,
		"http.ListenAndServe": true, "http.Serve": true, "exec.Command.Run": true,
	}
	capabilityPackages := map[string]bool{"os": true, "net": true, "net/http": true, "net/rpc": true, "os/exec": true, "syscall": true}
	capabilityMembers := map[string]bool{"os.Open": true, "os.Create": true, "os.ReadFile": true, "os.WriteFile": true, "os.Remove": true, "os.RemoveAll": true, "os.Rename": true, "os.Mkdir": true, "os.MkdirAll": true, "os.ReadDir": true, "os.Stat": true, "os.Lstat": true, "os.Chdir": true, "os.Getwd": true, "os.Getenv": true, "os.Setenv": true, "os.Unsetenv": true, "os.Environ": true, "os.StartProcess": true, "os.OpenFile": true, "os.CreateTemp": true, "os.MkdirTemp": true, "os.Executable": true, "os.UserHomeDir": true, "os.Hostname": true}

	var out []Diagnostic
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, rel, data, parser.ParseComments|parser.AllErrors)
	if err != nil {
		return out
	}
	inTest := strings.HasSuffix(rel, "_test.go")
	capFile := strings.HasSuffix(strings.TrimSpace(string(data[:fset.Position(file.Package).Offset])), "// strictgo: capability")
	imports := map[string]string{}
	for _, imp := range file.Imports {
		pkg, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		alias := filepath.Base(pkg)
		if imp.Name != nil {
			alias = imp.Name.Name
		}
		imports[alias] = pkg
		if alias == "." {
			out = append(out, sourceDiag(fset, rel, data, imp.Pos(), imp.End(), "strictgo::no_dot_import", "name imported packages explicitly"))
		}
	}
	add := func(node ast.Node, code, message string) {
		out = append(out, sourceDiag(fset, rel, data, node.Pos(), node.End(), code, message))
	}
	if !inTest {
		for _, decl := range file.Decls {
			if d, ok := decl.(*ast.GenDecl); ok && d.Tok == token.VAR {
				add(d, "strictgo::no_mutable_global", "avoid package-level variables; pass state explicitly")
			}
		}
	}
	ast.Inspect(file, func(node ast.Node) bool {
		if !inTest {
			if call, ok := node.(*ast.CallExpr); ok {
				if name := callName(call.Fun); name == "panic" {
					add(call, "strictgo::no_panic", "return an error instead of panicking")
				}
			}
			if stmt, ok := node.(*ast.ExprStmt); ok {
				if call, ok := stmt.X.(*ast.CallExpr); ok {
					name := qualifiedName(call.Fun, imports)
					if knownErrorCalls[name] {
						add(stmt, "strictgo::must_handle", "handle the result of "+name)
					}
				}
			}
		}
		if !capFile && !inTest {
			if sel, ok := node.(*ast.SelectorExpr); ok {
				if id, ok := sel.X.(*ast.Ident); ok {
					pkg := imports[id.Name]
					name := pkg + "." + sel.Sel.Name
					if (pkg == "os" && capabilityMembers[name]) || (pkg != "os" && capabilityPackages[pkg] && pkg != "") {
						add(sel, "strictgo::capability_boundary", "move "+name+" into a file marked // strictgo: capability")
					}
				}
			}
		}
		return true
	})
	return out
}
func callName(expr ast.Expr) string {
	switch x := expr.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.SelectorExpr:
		return callName(x.X) + "." + x.Sel.Name
	}
	return ""
}
func qualifiedName(expr ast.Expr, imports map[string]string) string {
	name := callName(expr)
	parts := strings.SplitN(name, ".", 2)
	if len(parts) == 2 {
		if pkg, ok := imports[parts[0]]; ok {
			return pkg + "." + parts[1]
		}
	}
	return name
}
func sourceDiag(fset *token.FileSet, rel string, data []byte, start, end token.Pos, code, message string) Diagnostic {
	s, e := fset.Position(start), fset.Position(end)
	lines := bytes.Split(data, []byte{'\n'})
	snippet := ""
	if s.Line > 0 && s.Line <= len(lines) {
		snippet = string(lines[s.Line-1])
	}
	return Diagnostic{Level: "error", Source: "strictgo", Code: code, Message: message, At: &Location{File: rel, Start: Position{Line: s.Line, Column: s.Column}, End: Position{Line: e.Line, Column: e.Column}, Snippet: snippet}}
}
func runGoTool(root string, args ...string) []Diagnostic {
	toolLine := regexp.MustCompile(`^(.+\.go):(\d+):(\d+):\s*(.+)$`)
	toolLineNoColumn := regexp.MustCompile(`^(.+\.go):(\d+):\s*(.+)$`)
	cmd := exec.Command("go", args...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	output, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	source := "go"
	if len(args) > 0 {
		source = "go " + args[0]
	}
	var out []Diagnostic
	scanner := bufio.NewScanner(bytes.NewReader(output))
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "# ") || strings.HasPrefix(line, "FAIL") || strings.HasPrefix(line, "? ") || strings.HasPrefix(line, "ok ") || strings.HasPrefix(line, "go: downloading ") {
			continue
		}
		if m := toolLine.FindStringSubmatch(line); m != nil {
			n, _ := strconv.Atoi(m[2])
			c, _ := strconv.Atoi(m[3])
			out = append(out, Diagnostic{Level: "error", Source: source, Code: "go::tool", Message: m[4], At: &Location{File: normalizeToolPath(root, m[1]), Start: Position{Line: n, Column: c}, End: Position{Line: n, Column: c}}})
			continue
		}
		if m := toolLineNoColumn.FindStringSubmatch(line); m != nil {
			n, _ := strconv.Atoi(m[2])
			out = append(out, Diagnostic{Level: "error", Source: source, Code: "go::tool", Message: m[3], At: &Location{File: normalizeToolPath(root, m[1]), Start: Position{Line: n, Column: 1}, End: Position{Line: n, Column: 1}}})
			continue
		}
		if strings.HasPrefix(line, "go:") || strings.Contains(line, "error") {
			out = append(out, Diagnostic{Level: "error", Source: source, Code: "go::tool", Message: line})
		}
	}
	if len(out) == 0 {
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = fmt.Sprint(err)
		}
		out = append(out, Diagnostic{Level: "error", Source: source, Code: "go::tool", Message: message})
	}
	return out
}
func normalizeToolPath(root, path string) string {
	if filepath.IsAbs(path) {
		if rel, err := filepath.Rel(root, path); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return filepath.ToSlash(rel)
		}
	}
	return filepath.ToSlash(path)
}
