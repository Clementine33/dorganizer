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
// library-relative path — a rescan renumbers nothing the caller navigates by —
// and DirID is that path's navigation identity, which is what a page address
// carries. The audio count is a status fact: a directory without audio is still
// listed and still browsable (ADR 0001 §1; ADR 0003 §2).
type dirResponse struct {
	Name           string `json:"name"`
	Path           string `json:"path"`
	RelPath        string `json:"rel_path"`
	DirID          string `json:"dir_id"`
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
			DirID:          dirID(lib.ID, lib.RootPath, d.RelPath),
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

// getMemberTree returns the stored tree of one member directory, addressed by
// its directory id. The resolved identity comes back with the tree, so a caller
// that only knows the identity still learns which path it names (ADR 0003 §4).
func (s *Server) getMemberTree(w http.ResponseWriter, r *http.Request) {
	member, ok := s.memberByDir(w, r)
	if !ok {
		return
	}
	entries, err := s.deps.Repo.ListEntriesUnderPath(member.absPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to list folder entries")
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Tree       *treeNode `json:"tree"`
		DirID      string    `json:"dir_id"`
		MemberPath string    `json:"member_path"`
	}{
		Tree:       buildMemberTree(member.absPath, entries),
		DirID:      member.dirID,
		MemberPath: member.relPath,
	})
}

// refreshMemberTree re-scans one member directory and answers with the
// refreshed tree. Refreshing is a scan, so it takes the scanning side of the
// admission control: a running file operation refuses it instead of letting
// two writers touch the same tree (ADR 0001 §3; ADR 0002 §2).
func (s *Server) refreshMemberTree(w http.ResponseWriter, r *http.Request) {
	member, ok := s.memberByDir(w, r)
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

	if err := s.deps.ScanService.RefreshMember(r.Context(), member.absPath, member.library.RootPath); err != nil {
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
	entries, err := s.deps.Repo.ListEntriesUnderPath(member.absPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to list folder entries")
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Tree       *treeNode `json:"tree"`
		DirID      string    `json:"dir_id"`
		MemberPath string    `json:"member_path"`
		Refreshed  bool      `json:"refreshed"`
	}{
		Tree:       buildMemberTree(member.absPath, entries),
		DirID:      member.dirID,
		MemberPath: member.relPath,
		Refreshed:  true,
	})
}

// resolvedMember is one member directory a request addressed by its directory
// id: the library it belongs to, the library-relative path the inventory stores
// as the member's identity, and the absolute path the repository queries by.
type resolvedMember struct {
	library *sqlite.Library
	relPath string
	absPath string
	dirID   string
}

// memberByDir resolves the ?dir= parameter of a member-scoped request. The
// identity is looked up among the direct child directories this library's
// inventory knows — an identity that names no directory of it, that is
// malformed, or that two directories claim, is refused — and only the single
// match is then validated on disk, so the workbench never invents a directory
// and a symlinked member is not a member (ADR 0001 §3; ADR 0003 §4).
func (s *Server) memberByDir(w http.ResponseWriter, r *http.Request) (*resolvedMember, bool) {
	lib, ok := s.library(w, r)
	if !ok {
		return nil, false
	}
	raw := r.URL.Query().Get("dir")
	if raw == "" {
		writeError(w, http.StatusBadRequest, "DIR_ID_REQUIRED", "dir must be a directory id")
		return nil, false
	}
	if !validDirID(raw) {
		writeError(w, http.StatusBadRequest, "DIR_ID_INVALID", "dir is not a directory id")
		return nil, false
	}
	children, err := s.deps.Repo.ListLibraryChildDirs(lib.RootPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to resolve the folder")
		return nil, false
	}
	rel, found, ambiguous := matchDirID(children, lib.ID, lib.RootPath, raw)
	if ambiguous {
		// Answering "not found" would hide a real state and answering with one
		// of the matches would be a guess (ADR 0003 §4).
		writeError(
			w,
			http.StatusConflict,
			"DIRECTORY_AMBIGUOUS",
			"more than one folder claims this id; reload the overview",
		)
		return nil, false
	}
	if !found {
		writeError(w, http.StatusNotFound, "DIRECTORY_NOT_FOUND", "the folder no longer exists")
		return nil, false
	}
	abs, err := fileops.ResolveMember(lib.RootPath, rel)
	if err != nil {
		writeMemberError(w, err)
		return nil, false
	}
	return &resolvedMember{
		library: lib,
		relPath: rel,
		absPath: pathnorm.NormalizeToPOSIX(abs),
		dirID:   dirID(lib.ID, lib.RootPath, rel),
	}, true
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
