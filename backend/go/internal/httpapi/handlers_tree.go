package httpapi

import (
	"errors"
	"net/http"
	"sort"
	"strings"

	"facette.io/natsort"

	"github.com/onsei/organizer/backend/internal/pathnorm"
	"github.com/onsei/organizer/backend/internal/repo/sqlite"
	"github.com/onsei/organizer/backend/internal/services/fileops"
	scanusecase "github.com/onsei/organizer/backend/internal/usecase/scan"
)

// dirResponse is one direct child directory of a library root. Identity is the
// library-relative path: a rescan renumbers nothing the caller navigates by,
// and the audio count is a status fact — a directory without audio is still
// listed and still browsable (spec N3, I1).
type dirResponse struct {
	Name           string `json:"name"`
	Path           string `json:"path"`
	RelPath        string `json:"rel_path"`
	AudioFileCount int    `json:"audio_file_count"`
	FileCount      int    `json:"file_count"`
}

// listLibraryDirs returns every direct child directory of the library root,
// including empty ones and ones holding no audio. The library-level recovery
// directory is not a member and is left out.
func (s *Server) listLibraryDirs(w http.ResponseWriter, r *http.Request) {
	lib, ok := s.library(w, r)
	if !ok {
		return
	}
	dirs, err := s.deps.Repo.ListLibraryDirs(lib.RootPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to list library folders")
		return
	}
	out := make([]dirResponse, 0, len(dirs))
	for _, d := range dirs {
		out = append(out, dirResponse{
			Name:           d.Name,
			Path:           d.Path,
			RelPath:        d.RelPath,
			AudioFileCount: d.AudioFileCount,
			FileCount:      d.FileCount,
		})
	}
	writeJSON(w, http.StatusOK, struct {
		Dirs []dirResponse `json:"dirs"`
	}{Dirs: out})
}

// treeNode is one node of a member tree. dir nodes carry children; file nodes
// carry size, bitrate (null when unknown) and format. RelPath is the path
// relative to the member root, which is what file management addresses.
type treeNode struct {
	Name     string      `json:"name"`
	Path     string      `json:"path"`
	RelPath  string      `json:"rel_path"`
	Type     string      `json:"type"`
	Size     *int64      `json:"size,omitempty"`
	Bitrate  *int32      `json:"bitrate"`
	Format   string      `json:"format"`
	Children []*treeNode `json:"children,omitempty"`
}

// getMemberTree returns the stored tree of one member directory. The member is
// addressed by its library-relative path and is resolved against the library
// root, so a path that is not a browsable member of this library is refused
// instead of resolving somewhere else.
func (s *Server) getMemberTree(w http.ResponseWriter, r *http.Request) {
	_, memberAbs, ok := s.member(w, r)
	if !ok {
		return
	}
	entries, err := s.deps.Repo.ListEntriesUnderPath(memberAbs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to list folder entries")
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Tree *treeNode `json:"tree"`
	}{Tree: buildMemberTree(memberAbs, entries)})
}

// refreshMemberTree re-scans one member directory and answers with the
// refreshed tree. Refreshing is a scan, so it takes the scanning side of the
// admission control: a running file operation refuses it instead of letting
// two writers touch the same tree (spec T2, C1).
func (s *Server) refreshMemberTree(w http.ResponseWriter, r *http.Request) {
	lib, memberAbs, ok := s.member(w, r)
	if !ok {
		return
	}
	if s.deps.ScanService == nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "scan service not configured")
		return
	}
	release, ok := s.beginScan(w)
	if !ok {
		return
	}
	defer release()

	if err := s.deps.ScanService.RefreshMember(r.Context(), memberAbs, lib.RootPath); err != nil {
		// The refresh failed; the caller keeps the tree it already shows and
		// says so. Nothing else was touched, so the failure is the whole
		// outcome.
		code, message := "REFRESH_FAILED", "failed to refresh the folder"
		if scanErr, isScanErr := scanusecase.AsError(err); isScanErr {
			code, message = scanErr.Code, scanErr.Message
		}
		writeError(w, http.StatusBadGateway, code, message)
		return
	}
	entries, err := s.deps.Repo.ListEntriesUnderPath(memberAbs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to list folder entries")
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Tree      *treeNode `json:"tree"`
		Refreshed bool      `json:"refreshed"`
	}{Tree: buildMemberTree(memberAbs, entries), Refreshed: true})
}

// member resolves the ?folder= parameter of a member-scoped request against
// the library root. A path that is not a plain relative member path, that
// names the recovery directory, or that does not exist as a real directory is
// refused: the workbench never invents a directory, and a symlinked member is
// not a member (spec T2).
func (s *Server) member(w http.ResponseWriter, r *http.Request) (*sqlite.Library, string, bool) {
	lib, ok := s.library(w, r)
	if !ok {
		return nil, "", false
	}
	rel := r.URL.Query().Get("folder")
	if _, ok := pathnorm.RelPath(rel); !ok {
		writeError(w, http.StatusBadRequest, "FOLDER_PATH_INVALID", "folder must be a library-relative path")
		return nil, "", false
	}
	abs, err := fileops.ResolveMember(lib.RootPath, rel)
	if err != nil {
		writeMemberError(w, err)
		return nil, "", false
	}
	return lib, pathnorm.NormalizeToPOSIX(abs), true
}

// writeMemberError maps a member resolution failure to its response.
func writeMemberError(w http.ResponseWriter, err error) {
	message := err.Error()
	switch {
	case strings.Contains(message, fileops.CodeMemberMissing):
		writeError(w, http.StatusNotFound, "MEMBER_MISSING", "the folder no longer exists")
	case strings.Contains(message, fileops.CodeSymlink):
		writeError(w, http.StatusBadRequest, "MEMBER_IS_SYMLINK", "symbolic links are not browsable")
	case strings.Contains(message, fileops.CodeMemberRoot):
		writeError(w, http.StatusBadRequest, "MEMBER_PATH_INVALID", "this path is not a browsable folder")
	default:
		writeError(w, http.StatusBadRequest, "MEMBER_PATH_INVALID", "this path is not a browsable folder")
	}
}

// beginScan registers one scanning operation with the admission gate; it
// answers the request and returns false when the scan must not start. The
// returned release is per-request state, never server state: one server serves
// concurrent requests.
func (s *Server) beginScan(w http.ResponseWriter) (func(), bool) {
	if s.deps.Gate == nil {
		return func() {}, true
	}
	release, err := s.deps.Gate.BeginScan()
	if err != nil {
		writeBusyError(w, err)
		return nil, false
	}
	return release, true
}

// beginManual takes the direct-file-management slot for a route that rewrites
// paths (a library root change or a library deletion) without writing files
// itself. A nil gate means this process has no file management wired; every
// test server is in that state.
func (s *Server) beginManual(w http.ResponseWriter) (func(), bool) {
	if s.deps.Gate == nil {
		return func() {}, true
	}
	release, err := s.deps.Gate.BeginManual()
	if err != nil {
		writeBusyError(w, err)
		return nil, false
	}
	return release, true
}

// busyMessage explains an admission refusal in the user's terms.
func busyMessage(err error) string {
	if busy, ok := errors.AsType[*fileops.BusyError](err); ok {
		switch busy.Reason {
		case "a scan is running":
			return "a scan is running; wait for it to finish"
		case "direct file management is in progress":
			return "direct file management is in progress; wait for it to finish"
		case "another file operation is in progress":
			return "another file operation is in progress; wait for it to finish"
		default:
			return busy.Reason
		}
	}
	return err.Error()
}

func writeBusyError(w http.ResponseWriter, err error) {
	writeError(w, http.StatusConflict, "BUSY", busyMessage(err))
}

// buildMemberTree assembles the nested node structure for the entries under a
// member directory. Directories sort before files; within the same type, both
// sort naturally by name. Node names are basenames only and RelPath is
// relative to the member root.
func buildMemberTree(rootPath string, entries []sqlite.EntryRow) *treeNode {
	index := map[string]*treeNode{}
	root := &treeNode{Name: basename(rootPath), Path: rootPath, RelPath: "", Type: "dir"}
	index[rootPath] = root

	for _, e := range entries {
		// The member root's own entry is already represented by the root node;
		// replacing it here would detach all children.
		if _, exists := index[e.Path]; exists {
			continue
		}
		node := &treeNode{Name: e.Name, Path: e.Path, RelPath: relativeToMember(rootPath, e.Path)}
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
