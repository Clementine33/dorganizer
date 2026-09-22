package inventory

import (
	"sort"
	"strings"

	"facette.io/natsort"

	"github.com/onsei/organizer/backend/internal/pathnorm"
)

// LibraryDir is one direct child directory of a library root as the workbench
// overview lists it: identity is the library-relative path (stable across
// rescans), and the audio count is a status fact — a directory without audio
// is still listed and still browsable.
type LibraryDir struct {
	Path           string
	Name           string
	RelPath        string
	AudioFileCount int
	FileCount      int
}

// TreeNode is one node of a member tree. Dir nodes carry children; file nodes
// carry size, bitrate (null when unknown) and format. RelPath is the path
// relative to the member root, which is what file management addresses.
type TreeNode struct {
	Name     string      `json:"name"`
	Path     string      `json:"path"`
	RelPath  string      `json:"rel_path"`
	Type     string      `json:"type"`
	Size     *int64      `json:"size,omitempty"`
	Bitrate  *int32      `json:"bitrate"`
	Format   string      `json:"format"`
	Children []*TreeNode `json:"children,omitempty"`
}

// BuildMemberTree assembles the nested node structure for the entries under a
// member directory. Directories sort before files; within the same type, both
// sort naturally by name. Node names are basenames only and RelPath is
// relative to the member root.
func BuildMemberTree(rootPath string, entries []Entry) *TreeNode {
	index := map[string]*TreeNode{}
	root := &TreeNode{Name: basename(rootPath), Path: rootPath, RelPath: "", Type: "dir"}
	index[rootPath] = root

	for _, e := range entries {
		// The member root's own entry is already represented by the root node;
		// replacing it here would detach all children.
		if _, exists := index[e.Path]; exists {
			continue
		}
		node := &TreeNode{Name: e.Name, Path: e.Path, RelPath: relativeToMember(rootPath, e.Path)}
		if e.IsDir {
			node.Type = "dir"
		} else {
			node.Type = "file"
			size := e.Size
			node.Size = &size
			if e.Bitrate != nil {
				bitrate := *e.Bitrate
				node.Bitrate = &bitrate
			}
			node.Format = e.Format
		}
		index[e.Path] = node

		// Entries are ordered by path, so a parent always precedes its children.
		if parent, ok := index[e.ParentPath]; ok {
			parent.Children = append(parent.Children, node)
		}
	}

	for _, node := range index {
		sort.SliceStable(node.Children, func(i, j int) bool {
			left, right := node.Children[i], node.Children[j]
			if left.Type != right.Type {
				return left.Type == "dir"
			}
			return natsort.Compare(left.Name, right.Name)
		})
	}
	return root
}

// relativeToMember renders an absolute entry path as member-relative POSIX.
func relativeToMember(memberPath, entryPath string) string {
	prefix := strings.TrimSuffix(pathnorm.NormalizeToPOSIX(memberPath), "/") + "/"
	normalized := pathnorm.NormalizeToPOSIX(entryPath)
	return strings.TrimPrefix(normalized, prefix)
}

// basename returns the last path segment of a POSIX path.
func basename(path string) string {
	if idx := strings.LastIndex(path, "/"); idx >= 0 {
		return path[idx+1:]
	}
	return path
}
