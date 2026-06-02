package authscan

import (
	"crypto/sha1"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
)

const (
	ModeRecursive = "recursive"
	ModeSubdirs   = "subdirs"
)

type File struct {
	Path string
	ID   string
}

type root struct {
	path          string
	logicalPrefix string
}

func Mode(cfg *config.Config) string {
	mode := ""
	if cfg != nil {
		mode = strings.ToLower(strings.TrimSpace(cfg.AuthScanMode))
	}
	if mode == "" {
		return ModeRecursive
	}
	return mode
}

func ListFiles(cfg *config.Config, authDir string) ([]File, []string) {
	roots, warnings := scanRoots(cfg, authDir)
	if len(roots) == 0 {
		return nil, warnings
	}

	seen := make(map[string]struct{})
	files := make([]File, 0)
	for _, r := range roots {
		err := filepath.WalkDir(r.path, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			if d.IsDir() {
				if path != r.path && d.Type()&os.ModeSymlink != 0 {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(strings.ToLower(d.Name()), ".json") {
				return nil
			}
			key := realFileKey(path)
			if _, ok := seen[key]; ok {
				return nil
			}
			seen[key] = struct{}{}
			files = append(files, File{Path: path, ID: fileID(r, path)})
			return nil
		})
		if err != nil {
			warnings = append(warnings, "scan auth directory "+r.path+": "+err.Error())
		}
	}
	sort.Slice(files, func(i, j int) bool {
		return strings.ToLower(files[i].ID) < strings.ToLower(files[j].ID)
	})
	return files, warnings
}

func WatchDirs(cfg *config.Config, authDir string) ([]string, []string) {
	roots, warnings := scanRoots(cfg, authDir)
	if len(roots) == 0 {
		return nil, warnings
	}
	seen := make(map[string]struct{})
	dirs := make([]string, 0)
	for _, r := range roots {
		err := filepath.WalkDir(r.path, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			if !d.IsDir() {
				return nil
			}
			if path != r.path && d.Type()&os.ModeSymlink != 0 {
				return filepath.SkipDir
			}
			key := realDirKey(path)
			if _, ok := seen[key]; ok {
				return nil
			}
			seen[key] = struct{}{}
			dirs = append(dirs, path)
			return nil
		})
		if err != nil {
			warnings = append(warnings, "scan auth watch directory "+r.path+": "+err.Error())
		}
	}
	sort.Strings(dirs)
	return dirs, warnings
}

func ContainsFile(cfg *config.Config, authDir, path string) bool {
	if strings.TrimSpace(path) == "" || !strings.HasSuffix(strings.ToLower(path), ".json") {
		return false
	}
	if _, ok := IDForPath(cfg, authDir, path); ok {
		return true
	}
	return false
}

func IDForPath(cfg *config.Config, authDir, path string) (string, bool) {
	path = filepath.Clean(path)
	for _, r := range mustScanRoots(cfg, authDir) {
		rel, err := filepath.Rel(r.path, path)
		if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || rel == ".." {
			continue
		}
		return joinID(r.logicalPrefix, rel), true
	}
	return "", false
}

func scanRoots(cfg *config.Config, authDir string) ([]root, []string) {
	warnings := make([]string, 0)
	if strings.TrimSpace(authDir) == "" {
		return nil, warnings
	}
	switch Mode(cfg) {
	case ModeRecursive:
		r, ok := recursiveRoot(authDir)
		if !ok {
			return nil, warnings
		}
		return []root{r}, warnings
	case ModeSubdirs:
		if cfg == nil || len(cfg.AuthScanDirs) == 0 {
			warnings = append(warnings, "auth-scan-mode is subdirs but auth-scan-dirs is empty")
			return nil, warnings
		}
		return subdirRoots(cfg, authDir, warnings)
	default:
		r, ok := recursiveRoot(authDir)
		if !ok {
			return nil, warnings
		}
		warnings = append(warnings, "unknown auth-scan-mode "+Mode(cfg)+", using recursive")
		return []root{r}, warnings
	}
}

func mustScanRoots(cfg *config.Config, authDir string) []root {
	roots, _ := scanRoots(cfg, authDir)
	return roots
}

func recursiveRoot(authDir string) (root, bool) {
	dir := filepath.Clean(authDir)
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return root{}, false
	}
	return root{path: dir}, true
}

func subdirRoots(cfg *config.Config, authDir string, warnings []string) ([]root, []string) {
	roots := make([]root, 0, len(cfg.AuthScanDirs))
	seen := make(map[string]struct{})
	base := filepath.Clean(authDir)
	for _, raw := range cfg.AuthScanDirs {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		candidate := raw
		if !filepath.IsAbs(candidate) {
			candidate = filepath.Join(base, candidate)
		}
		candidate = filepath.Clean(candidate)
		resolved, err := filepath.EvalSymlinks(candidate)
		if err != nil {
			warnings = append(warnings, "resolve auth-scan-dir "+raw+": "+err.Error())
			continue
		}
		info, err := os.Stat(resolved)
		if err != nil || !info.IsDir() {
			continue
		}
		key := realDirKey(resolved)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		roots = append(roots, root{path: filepath.Clean(resolved), logicalPrefix: logicalPrefix(base, candidate, resolved)})
	}
	return roots, warnings
}

func logicalPrefix(authDir, candidate, resolved string) string {
	if rel, ok := relativeUnder(authDir, candidate); ok {
		return rel
	}
	if rel, ok := relativeUnder(authDir, resolved); ok {
		return rel
	}
	base := filepath.Base(candidate)
	if base == "." || base == string(os.PathSeparator) || base == "" {
		base = filepath.Base(resolved)
	}
	sum := sha1.Sum([]byte(resolved))
	return filepath.Join("external", base+"-"+hex.EncodeToString(sum[:])[:8])
}

func relativeUnder(base, path string) (string, bool) {
	rel, err := filepath.Rel(filepath.Clean(base), filepath.Clean(path))
	if err != nil || rel == "." || rel == "" || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", false
	}
	return rel, true
}

func fileID(r root, path string) string {
	rel, err := filepath.Rel(r.path, path)
	if err != nil || rel == "." {
		rel = filepath.Base(path)
	}
	return joinID(r.logicalPrefix, rel)
}

func joinID(prefix, rel string) string {
	rel = filepath.Clean(rel)
	if prefix == "" || prefix == "." {
		return rel
	}
	return filepath.Join(prefix, rel)
}

func realFileKey(path string) string {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		return filepath.Clean(real)
	}
	return filepath.Clean(path)
}

func realDirKey(path string) string {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		return filepath.Clean(real)
	}
	return filepath.Clean(path)
}
