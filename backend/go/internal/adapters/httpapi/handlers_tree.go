package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/onsei/organizer/backend/internal/admission"
	"github.com/onsei/organizer/backend/internal/inventory"
	"github.com/onsei/organizer/backend/internal/library"
	"github.com/onsei/organizer/backend/internal/services/fileops"
)

// dirResponse is one direct child directory of a library root: the observed
// facts plus the identity a page address carries.
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
	dirs, err := s.deps.Library.Dirs(lib)
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
			DirID:          d.DirID,
			AudioFileCount: d.AudioFileCount,
			FileCount:      d.FileCount,
		})
	}
	writeJSON(w, http.StatusOK, struct {
		Dirs []dirResponse `json:"dirs"`
	}{Dirs: out})
}

// treeResponse is the stored tree of one member directory plus the identity it
// was addressed by, so a caller that only knows the identity learns which path
// it names (ADR 0003 §4).
type treeResponse struct {
	Tree       *inventory.TreeNode `json:"tree"`
	DirID      string              `json:"dir_id"`
	MemberPath string              `json:"member_path"`
	Refreshed  bool                `json:"refreshed,omitempty"`
}

// getMemberTree returns the stored tree of one member directory, addressed by
// its directory id.
func (s *Server) getMemberTree(w http.ResponseWriter, r *http.Request) {
	member, ok := s.memberByDir(w, r)
	if !ok {
		return
	}
	tree, err := s.deps.Library.Tree(member)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to list folder entries")
		return
	}
	writeJSON(w, http.StatusOK, treeResponse{
		Tree:       tree,
		DirID:      member.DirID,
		MemberPath: member.RelPath,
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
	if s.deps.Inventory == nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "scan service not configured")
		return
	}
	release, admitErr := s.deps.Inventory.AdmitMemberRefresh()
	if admitErr != nil {
		writeScanAdmissionError(w, admitErr)
		return
	}
	defer release()

	if err := s.deps.Inventory.RefreshMember(r.Context(), member.AbsPath, member.Library.RootPath); err != nil {
		// The refresh failed; the caller keeps the tree it already shows and
		// says so. Nothing else was touched, so the failure is the whole
		// outcome.
		code, message := "REFRESH_FAILED", "failed to refresh the folder"
		if scanErr, isScanErr := inventory.AsError(err); isScanErr {
			code, message = scanErr.Code, scanErr.Message
		}
		writeError(w, http.StatusBadGateway, code, message)
		return
	}
	tree, err := s.deps.Library.Tree(member)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to list folder entries")
		return
	}
	writeJSON(w, http.StatusOK, treeResponse{
		Tree:       tree,
		DirID:      member.DirID,
		MemberPath: member.RelPath,
		Refreshed:  true,
	})
}

// memberByDir resolves the ?dir= parameter of a member-scoped request through
// the library entry, which owns the identity and the disk rules behind it, and
// answers the refusal itself.
func (s *Server) memberByDir(w http.ResponseWriter, r *http.Request) (*library.Member, bool) {
	lib, ok := s.library(w, r)
	if !ok {
		return nil, false
	}
	member, err := s.deps.Library.Member(lib, r.URL.Query().Get("dir"))
	if err != nil {
		writeResolveError(w, err)
		return nil, false
	}
	return member, true
}

// writeResolveError maps a refused member resolution to its response: the
// library's own codes first, then the member-path codes direct file management
// owns (a member that vanished, is a symlink, or is not a browsable folder).
func writeResolveError(w http.ResponseWriter, err error) {
	if libErr, ok := library.AsError(err); ok {
		switch libErr.Kind {
		case library.ErrKindInvalidArgument:
			writeError(w, http.StatusBadRequest, libErr.Code, libErr.Message)
		case library.ErrKindNotFound:
			writeError(w, http.StatusNotFound, libErr.Code, libErr.Message)
		case library.ErrKindConflict:
			writeError(w, http.StatusConflict, libErr.Code, libErr.Message)
		default:
			writeError(w, http.StatusInternalServerError, libErr.Code, libErr.Message)
		}
		return
	}
	writeMemberError(w, err)
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

// busyMessage explains an admission refusal in the user's terms.
func busyMessage(err error) string {
	if busy, ok := errors.AsType[*admission.BusyError](err); ok {
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
