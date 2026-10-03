package main

import (
	"bufio"
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/token"
	"go/types"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/ilvar/strictgo/internal/capability"
	"golang.org/x/tools/go/packages"
)

type sourceFile struct {
	path, rel string
	data      []byte
	file      *ast.File
	pkg       *packages.Package
}

func check(path string, fix bool) (Report, error) {
	root, err := moduleRoot(path)
	if err != nil {
		return Report{}, err
	}
	for pass := 0; pass < 5; pass++ {
		report, err := checkOnce(root)
		if err != nil {
			return Report{}, err
		}
		if !fix || report.OK || pass == 4 {
			return report, nil
		}
		changed, err := applyFixes(root, report.Diagnostics)
		if err != nil {
			return Report{}, err
		}
		if !changed {
			return report, nil
		}
	}
	return Report{}, fmt.Errorf("fix iteration limit reached")
}
func checkOnce(root string) (Report, error) {
	report := Report{Diagnostics: []Diagnostic{}}
	cfg := &packages.Config{Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo | packages.NeedImports, Dir: root, Tests: true}
	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		return Report{}, err
	}
	files := map[string]sourceFile{}
	for _, pkg := range pkgs {
		if pkg.Fset == nil {
			continue
		}
		for i, file := range pkg.Syntax {
			if i >= len(pkg.CompiledGoFiles) {
				continue
			}
			path := pkg.CompiledGoFiles[i]
			if !capability.IsWithin(root, path) || !strings.HasSuffix(path, ".go") {
				continue
			}
			if _, exists := files[path]; exists {
				continue
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return Report{}, err
			}
			data, err := capability.ReadFile(path)
			if err != nil {
				return Report{}, err
			}
			files[path] = sourceFile{path, filepath.ToSlash(rel), data, file, pkg}
		}
	}
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		file := files[path]
		formatted, err := format.Source(file.data)
		if err == nil && !bytes.Equal(file.data, formatted) {
			report.Diagnostics = append(report.Diagnostics, Diagnostic{Level: "error", Source: "strictgo", Code: "strictgo::format", Message: "file is not gofmt formatted", At: &Location{File: file.rel, Start: Position{1, 1}, End: Position{1, 1}}, Fixes: []Fix{{Message: "Apply gofmt", Edits: []Edit{{File: file.rel, StartByte: 0, EndByte: len(file.data), Replacement: string(formatted)}}}}})
		}
		report.Diagnostics = append(report.Diagnostics, scanSource(file)...)
	}
	report.Diagnostics = append(report.Diagnostics, runGoTool(root, "test", "-run", "^$", "./...")...)
	report.Diagnostics = append(report.Diagnostics, runGoTool(root, "vet", "./...")...)
	finishReport(&report)
	return report, nil
}
func finishReport(report *Report) {
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
	report.ErrorCount = 0
	report.WarningCount = 0
	for _, d := range report.Diagnostics {
		if d.Level == "error" {
			report.ErrorCount++
		} else if d.Level == "warning" {
			report.WarningCount++
		}
	}
	report.OK = report.ErrorCount == 0
	if report.Diagnostics == nil {
		report.Diagnostics = []Diagnostic{}
	}
}
func scanSource(src sourceFile) []Diagnostic {
	var out []Diagnostic
	file, pkg := src.file, src.pkg
	inTest := strings.HasSuffix(src.path, "_test.go")
	capPackage := filepath.Base(filepath.Dir(src.path)) == "capability" && file.Name.Name == "capability"
	add := func(node ast.Node, code, message string, fixes ...Fix) {
		d := sourceDiag(pkg.Fset, src.rel, src.data, node.Pos(), node.End(), code, message)
		d.Fixes = fixes
		out = append(out, d)
	}
	for _, imp := range file.Imports {
		if imp.Name == nil || imp.Name.Name != "." {
			continue
		}
		fixes := dotImportFix(src, imp)
		add(imp, "strictgo::no_dot_import", "name imported packages explicitly", fixes...)
	}
	if !inTest {
		for _, decl := range file.Decls {
			if d, ok := decl.(*ast.GenDecl); ok && d.Tok == token.VAR {
				add(d, "strictgo::no_mutable_global", "avoid package-level variables; pass state explicitly")
			}
		}
	}
	ast.Inspect(file, func(node ast.Node) bool {
		if inTest {
			return true
		}
		if call, ok := node.(*ast.CallExpr); ok {
			if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "panic" && pkg.TypesInfo.Uses[id] == types.Universe.Lookup("panic") {
				add(call, "strictgo::no_panic", "return an error instead of panicking")
			}
		}
		if stmt, ok := node.(*ast.ExprStmt); ok {
			if call, ok := stmt.X.(*ast.CallExpr); ok && returnsError(pkg.TypesInfo.TypeOf(call)) {
				add(stmt, "strictgo::must_handle", "handle the returned error or explicitly discard it")
			}
		}
		if !capPackage {
			if sel, ok := node.(*ast.SelectorExpr); ok {
				if name := capabilityCall(pkg.TypesInfo, sel); name != "" {
					add(sel, "strictgo::capability_boundary", "move "+name+" into a package named capability")
				}
			}
		}
		return true
	})
	return out
}
func returnsError(t types.Type) bool {
	if t == nil {
		return false
	}
	errType := types.Universe.Lookup("error").Type()
	if tuple, ok := t.(*types.Tuple); ok {
		for i := 0; i < tuple.Len(); i++ {
			if types.AssignableTo(tuple.At(i).Type(), errType) {
				return true
			}
		}
		return false
	}
	return types.AssignableTo(t, errType)
}
func capabilityCall(info *types.Info, sel *ast.SelectorExpr) string {
	if id, ok := sel.X.(*ast.Ident); ok {
		if pkgName, ok := info.Uses[id].(*types.PkgName); ok {
			path := pkgName.Imported().Path()
			name := sel.Sel.Name
			switch path {
			case "os":
				if map[string]bool{"Open": true, "OpenRoot": true, "Create": true, "ReadFile": true, "WriteFile": true, "Remove": true, "RemoveAll": true, "Rename": true, "Mkdir": true, "MkdirAll": true, "ReadDir": true, "Stat": true, "Lstat": true, "Chdir": true, "Getwd": true, "Getenv": true, "Setenv": true, "Unsetenv": true, "Environ": true, "StartProcess": true, "OpenFile": true, "CreateTemp": true, "MkdirTemp": true, "Executable": true, "UserHomeDir": true, "Hostname": true, "Args": true, "Exit": true, "Stdout": true, "Stderr": true}[name] {
					return path + "." + name
				}
			case "os/exec", "syscall":
				return path + "." + name
			case "net":
				if strings.HasPrefix(name, "Dial") || strings.HasPrefix(name, "Listen") || strings.HasPrefix(name, "Lookup") {
					return path + "." + name
				}
			case "net/http":
				if map[string]bool{"Get": true, "Post": true, "PostForm": true, "Head": true, "ListenAndServe": true, "ListenAndServeTLS": true, "Serve": true, "ServeTLS": true}[name] {
					return path + "." + name
				}
			case "path/filepath":
				if map[string]bool{"Walk": true, "WalkDir": true, "Glob": true, "EvalSymlinks": true}[name] {
					return path + "." + name
				}
			}
		}
	}
	if selection := info.Selections[sel]; selection != nil {
		t := selection.Recv()
		for {
			ptr, ok := t.(*types.Pointer)
			if !ok {
				break
			}
			t = ptr.Elem()
		}
		if named, ok := t.(*types.Named); ok && named.Obj() != nil && named.Obj().Pkg() != nil {
			path, name, method := named.Obj().Pkg().Path(), named.Obj().Name(), sel.Sel.Name
			operations := map[string]map[string]bool{
				"os.File":     {"Read": true, "ReadAt": true, "ReadDir": true, "Readdir": true, "Readdirnames": true, "Write": true, "WriteAt": true, "WriteString": true, "Close": true, "Sync": true, "Seek": true, "Stat": true, "Truncate": true, "Chmod": true, "Chown": true},
				"os.Root":     {"Open": true, "Create": true, "OpenFile": true, "ReadFile": true, "WriteFile": true, "Mkdir": true, "MkdirAll": true, "Remove": true, "RemoveAll": true, "Stat": true, "Lstat": true, "Close": true},
				"os/exec.Cmd": {"Run": true, "Start": true, "Wait": true, "Output": true, "CombinedOutput": true, "StdinPipe": true, "StdoutPipe": true, "StderrPipe": true},
				"net.Dialer":  {"Dial": true, "DialContext": true}, "net.ListenConfig": {"Listen": true, "ListenPacket": true},
				"net/http.Client": {"Do": true, "Get": true, "Head": true, "Post": true, "PostForm": true, "CloseIdleConnections": true},
				"net/http.Server": {"ListenAndServe": true, "ListenAndServeTLS": true, "Serve": true, "ServeTLS": true, "Shutdown": true, "Close": true},
			}
			if operations[path+"."+name][method] {
				return path + "." + name + "." + method
			}
		}
	}

	return ""
}
func dotImportFix(src sourceFile, imp *ast.ImportSpec) []Fix {
	path, err := strconv.Unquote(imp.Path.Value)
	if err != nil {
		return nil
	}
	imported := src.pkg.Imports[path]
	if imported == nil || imported.Types == nil || src.pkg.TypesInfo == nil {
		return nil
	}
	base := "strictgo_" + imported.Name
	identifiers := map[string]bool{}
	ast.Inspect(src.file, func(node ast.Node) bool {
		if id, ok := node.(*ast.Ident); ok {
			identifiers[id.Name] = true
		}
		return true
	})
	alias := base
	for n := 2; identifiers[alias]; n++ {
		alias = base + strconv.Itoa(n)
	}
	edits := []Edit{{File: src.rel, StartByte: src.pkg.Fset.Position(imp.Name.Pos()).Offset, EndByte: src.pkg.Fset.Position(imp.Name.End()).Offset, Replacement: alias}}
	ast.Inspect(src.file, func(node ast.Node) bool {
		if id, ok := node.(*ast.Ident); ok {
			obj := src.pkg.TypesInfo.Uses[id]
			if obj != nil && obj.Pkg() != nil && obj.Pkg().Path() == path {
				offset := src.pkg.Fset.Position(id.Pos()).Offset
				edits = append(edits, Edit{File: src.rel, StartByte: offset, EndByte: offset, Replacement: alias + "."})
			}
		}
		return true
	})
	if len(edits) == 1 {
		return nil
	}
	return []Fix{{Message: "Qualify dot-imported names", Edits: edits}}
}
func sourceDiag(fset *token.FileSet, rel string, data []byte, start, end token.Pos, code, message string) Diagnostic {
	s, e := fset.Position(start), fset.Position(end)
	lines := bytes.Split(data, []byte{'\n'})
	snippet := ""
	if s.Line > 0 && s.Line <= len(lines) {
		snippet = string(lines[s.Line-1])
	}
	return Diagnostic{Level: "error", Source: "strictgo", Code: code, Message: message, At: &Location{File: rel, Start: Position{s.Line, s.Column}, End: Position{e.Line, e.Column}, Snippet: snippet}}
}
func runGoTool(root string, args ...string) []Diagnostic {
	output, err := capability.RunGo(root, args...)
	if err == nil {
		return nil
	}
	source := "go"
	if len(args) > 0 {
		source = "go " + args[0]
	}
	toolLine := regexp.MustCompile(`^(.+\.go):(\d+):(\d+):\s*(.+)$`)
	toolLineNoColumn := regexp.MustCompile(`^(.+\.go):(\d+):\s*(.+)$`)
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
			out = append(out, Diagnostic{Level: "error", Source: source, Code: "go::tool", Message: m[4], At: &Location{File: normalizeToolPath(root, m[1]), Start: Position{n, c}, End: Position{n, c}}})
			continue
		}
		if m := toolLineNoColumn.FindStringSubmatch(line); m != nil {
			n, _ := strconv.Atoi(m[2])
			out = append(out, Diagnostic{Level: "error", Source: source, Code: "go::tool", Message: m[3], At: &Location{File: normalizeToolPath(root, m[1]), Start: Position{n, 1}, End: Position{n, 1}}})
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
