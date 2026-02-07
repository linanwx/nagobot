package health

import (
	"os"
	"path/filepath"
	"sort"
)

func buildWorkspaceTree(root string, depth, maxEntries int) *WorkspaceTree {
	if depth <= 0 {
		depth = 2
	}
	if maxEntries <= 0 {
		maxEntries = 200
	}

	tree := &WorkspaceTree{
		Root:       root,
		Depth:      depth,
		MaxEntries: maxEntries,
		Entries:    []TreeEntry{},
	}

	stat, err := os.Stat(root)
	if err != nil {
		tree.Error = err.Error()
		return tree
	}
	if !stat.IsDir() {
		tree.Error = "workspace is not a directory"
		return tree
	}

	var walk func(absDir, relDir string, level int)
	walk = func(absDir, relDir string, level int) {
		if tree.Truncated {
			return
		}

		dirEntries, readErr := os.ReadDir(absDir)
		if readErr != nil {
			return
		}
		sort.Slice(dirEntries, func(i, j int) bool {
			return dirEntries[i].Name() < dirEntries[j].Name()
		})

		for _, de := range dirEntries {
			if tree.Truncated {
				return
			}

			name := de.Name()
			if de.IsDir() && shouldSkipDir(name) {
				continue
			}

			relPath := name
			if relDir != "" {
				relPath = filepath.Join(relDir, name)
			}
			relPath = filepath.ToSlash(relPath)

			entry := TreeEntry{
				Path: relPath,
				Type: "file",
			}
			if de.IsDir() {
				entry.Type = "dir"
			} else if info, infoErr := de.Info(); infoErr == nil {
				entry.SizeBytes = info.Size()
			}

			tree.Entries = append(tree.Entries, entry)
			if len(tree.Entries) >= tree.MaxEntries {
				tree.Truncated = true
				return
			}

			if de.IsDir() && level+1 < depth {
				walk(filepath.Join(absDir, name), relPath, level+1)
			}
		}
	}

	walk(root, "", 0)
	return tree
}

func shouldSkipDir(name string) bool {
	switch name {
	case ".git", "node_modules", "vendor", ".tmp":
		return true
	default:
		return false
	}
}
