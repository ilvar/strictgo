package capability

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func Args() []string                       { return os.Args[1:] }
func Exit(code int)                        { os.Exit(code) }
func Stdout(data []byte) error             { _, err := os.Stdout.Write(data); return err }
func Stderr(message string)                { _, _ = fmt.Fprintln(os.Stderr, message) }
func ReadFile(path string) ([]byte, error) { return os.ReadFile(path) }
func WriteFile(path string, data []byte) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".strictgo-*.tmp")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(temporary.Name()) }()
	if err := temporary.Chmod(info.Mode().Perm()); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporary.Name(), path)
}

func RunGo(root string, args ...string) ([]byte, error) {
	cmd := exec.Command("go", args...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	return cmd.CombinedOutput()
}
func ModuleRoot(path string) (string, error) {
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
func CreateProject(name string, files map[string]string) error {
	if _, err := os.Lstat(name); err == nil {
		return fmt.Errorf("%s already exists", name)
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.Mkdir(name, 0755); err != nil {
		return err
	}
	for path, content := range files {
		full := filepath.Join(name, path)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			return err
		}
		if err := os.WriteFile(full, []byte(content), 0644); err != nil {
			return err
		}
	}
	return nil
}
func InstallSkills(skill string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	type client struct{ name, detect, command, target string }
	clients := []client{{"Codex", ".codex", "codex", filepath.Join(home, ".agents", "skills", "strictgo", "SKILL.md")}, {"Claude Code", ".claude", "claude", filepath.Join(home, ".claude", "skills", "strictgo", "SKILL.md")}}
	found := 0
	var pending []client
	for _, c := range clients {
		_, dirErr := os.Stat(filepath.Join(home, c.detect))
		_, cmdErr := exec.LookPath(c.command)
		if dirErr != nil && cmdErr != nil {
			continue
		}
		found++
		prior, err := os.ReadFile(c.target)
		if err == nil {
			if string(prior) != skill {
				return fmt.Errorf("refusing to overwrite modified skill at %s", c.target)
			}
			continue
		}
		if !os.IsNotExist(err) {
			return err
		}
		pending = append(pending, c)
	}
	if found == 0 {
		return errors.New("no supported agent installation detected")
	}
	for _, c := range pending {
		if err := os.MkdirAll(filepath.Dir(c.target), 0755); err != nil {
			return err
		}
		if err := os.WriteFile(c.target, []byte(skill), 0644); err != nil {
			return err
		}
		Stderr("installed " + c.name + " skill: " + c.target)
	}
	return nil
}
func IsWithin(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func ResolvePath(path string) (string, error) { return filepath.EvalSymlinks(path) }
