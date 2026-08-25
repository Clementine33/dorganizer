package httpapi

import (
	"errors"
	"net/http"
	"sort"
	"strings"

	"facette.io/natsort"
	"github.com/onsei/organizer/backend/internal/repo/sqlite"
)

// folderResponse is the JSON shape of a library folder in list responses.
type folderResponse struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Path           string `json:"path"`
	RelativePath   string `json:"relative_path"`
	AudioFileCount int    `json:"audio_file_count"`
}

func toFolderResponse(f *sqlite.LibraryFolder) folderResponse {
	return folderResponse{
		ID:             f.ID,
		Name:           f.Name,
		Path:           f.Path,
		RelativePath:   f.RelativePath,
		AudioFileCount: f.AudioFileCount,
	}
}

// listLibraryFolders returns the direct-child folders of a library.
func (s *Server) listLibraryFolders(w http.ResponseWriter, r *http.Request) {
	lib, err := s.deps.Repo.GetLibrary(r.PathValue("id"))
	if err != nil {
		if errors.Is(err, sqlite.ErrLibraryNotFound) {
			writeError(w, http.StatusNotFound, "LIBRARY_NOT_FOUND", "library not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to load library")
		return
	}

	folders, err := s.deps.Repo.ListLibraryFolders(lib.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to list folders")
		return
	}
	out := make([]folderResponse, 0, len(folders))
	for _, f := range folders {
		out = append(out, toFolderResponse(f))
	}
	writeJSON(w, http.StatusOK, struct {
		Folders []folderResponse `json:"folders"`
	}{Folders: out})
}

// treeNode is one node of the folder tree response. dir nodes carry children;
// file nodes carry size, bitrate (null when unknown) and format.
type treeNode struct {
	Name     string      `json:"name"`
	Path     string      `json:"path"`
	Type     string      `json:"type"`
	Size     *int64      `json:"size,omitempty"`
	Bitrate  *int32      `json:"bitrate"`
	Format   string      `json:"format"`
	Children []*treeNode `json:"children,omitempty"`
}

// getFolderTree returns the recursive tree of a library folder. The folder is
// resolved within the library, so a folder belonging to another library 404s.
func (s *Server) getFolderTree(w http.ResponseWriter, r *http.Request) {
	lib, err := s.deps.Repo.GetLibrary(r.PathValue("id"))
	if err != nil {
		if errors.Is(err, sqlite.ErrLibraryNotFound) {
			writeError(w, http.StatusNotFound, "LIBRARY_NOT_FOUND", "library not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to load library")
		return
	}

	folder, err := s.deps.Repo.GetLibraryFolder(lib.ID, r.PathValue("folderId"))
	if err != nil {
		if errors.Is(err, sqlite.ErrLibraryFolderNotFound) {
			writeError(w, http.StatusNotFound, "FOLDER_NOT_FOUND", "folder not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to load folder")
		return
	}

	entries, err := s.deps.Repo.ListEntriesUnderPath(folder.Path)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to list folder entries")
		return
	}
	root := buildTree(folder.Path, entries)
	writeJSON(w, http.StatusOK, struct {
		Tree *treeNode `json:"tree"`
	}{Tree: root})
}

// buildTree assembles the nested node structure for the entries under a
// folder path. Directories sort before files; within the same type, both sort
// naturally by name. Node names are basenames only.
func buildTree(rootPath string, entries []sqlite.EntryRow) *treeNode {
	index := map[string]*treeNode{}
	root := &treeNode{Name: basename(rootPath), Path: rootPath, Type: "dir"}
	index[rootPath] = root

	for _, e := range entries {
		// The folder root's own entry is already represented by the root
		// node; replacing it here would detach all children.
		if _, exists := index[e.Path]; exists {
			continue
		}
		node := &treeNode{Name: e.Name, Path: e.Path}
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

		// Entries are ordered by path, so a parent always precedes its
		// children and the folder root's own entry is skipped (its parent is
		// outside the tree).
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

// basename returns the last path segment of a POSIX path.
func basename(path string) string {
	if idx := strings.LastIndex(path, "/"); idx >= 0 {
		return path[idx+1:]
	}
	return path
}
