package library

import (
	"errors"

	"github.com/onsei/organizer/backend/internal/inventory"
)

// InventoryReader is the inventory read side the library browses through: the
// observed entries a library's directories hold. The observed facts belong to
// the inventory (it is the only writer of that table); the library owns what
// the overview lists and which identity each directory carries.
type InventoryReader interface {
	// ListLibraryDirs returns the direct child directories of a library root,
	// the recovery directory among them.
	ListLibraryDirs(rootPath string) ([]*inventory.LibraryDir, error)
	// ListLibraryChildDirs returns the library-relative paths of the direct
	// child directories: the candidates an identity resolves against.
	ListLibraryChildDirs(rootPath string) ([]string, error)
	// ListEntriesUnderPath returns the stored entries at or beneath a path.
	ListEntriesUnderPath(pathPrefix string) ([]inventory.Entry, error)
}

// ResolveMember validates one library-relative member path on disk and returns
// its absolute path. The member rules — inside the root, not a symlink, exists,
// not the recovery directory — belong to direct file management, which lends
// them here so that browsing and writing agree on what a member is.
type ResolveMember func(rootPath, memberRel string) (string, error)

// Dir is one direct child directory as the overview lists it: the inventory's
// observed facts plus the navigation identity this library derives for it.
type Dir struct {
	inventory.LibraryDir

	DirID string
}

// Member is one member directory a request addressed by its directory id.
type Member struct {
	Library *Library
	// RelPath is the library-relative path: the member's durable data identity.
	RelPath string
	// AbsPath is the path the inventory queries by.
	AbsPath string
	// DirID is the navigation identity the request named (ADR 0003 §2).
	DirID string
}

// Error is one refusal the browse path answers with. It carries the stable
// code the HTTP layer maps to a status, so a caller can branch on the code
// rather than on a message (the way the module's plain sentinels are used for
// the library rows themselves).
type Error struct {
	Kind    string
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Kind + ": " + e.Message }

// Error kinds for browse refusals.
const (
	ErrKindInvalidArgument = "invalid_argument"
	ErrKindNotFound        = "not_found"
	ErrKindConflict        = "conflict"
	ErrKindInternal        = "internal"
)

// AsError extracts a browse *Error from an error chain.
func AsError(err error) (*Error, bool) {
	e, ok := errors.AsType[*Error](err)
	return e, ok
}

// Dirs lists the direct child directories of one library as the overview
// shows them: every child directory, empty or audio-less ones included, each
// with the identity a page address carries. The recovery directory is the
// inventory's own boundary and stays in the listing; callers that must not
// offer it (member resolution) filter it themselves.
func (s *Service) Dirs(lib *Library) ([]Dir, error) {
	found, err := s.inventory.ListLibraryDirs(lib.RootPath)
	if err != nil {
		return nil, err
	}
	out := make([]Dir, 0, len(found))
	for _, d := range found {
		out = append(out, Dir{
			LibraryDir: *d,
			DirID:      DirID(lib.ID, lib.RootPath, d.RelPath),
		})
	}
	return out, nil
}

// Member resolves the directory id of a member-scoped request. The identity is
// looked up among the direct child directories this library's inventory knows —
// an identity that names none of them, that is malformed, or that two
// directories claim, is refused — and only the single match is then validated
// on disk, so the workbench never invents a directory and a symlinked member is
// not a member (ADR 0001 §3; ADR 0003 §4).
func (s *Service) Member(lib *Library, rawDirID string) (*Member, error) {
	if rawDirID == "" {
		return nil, &Error{ErrKindInvalidArgument, "DIR_ID_REQUIRED", "dir must be a directory id"}
	}
	if !ValidDirID(rawDirID) {
		return nil, &Error{ErrKindInvalidArgument, "DIR_ID_INVALID", "dir is not a directory id"}
	}
	children, err := s.inventory.ListLibraryChildDirs(lib.RootPath)
	if err != nil {
		return nil, err
	}
	rel, found, ambiguous := MatchDirID(children, lib.ID, lib.RootPath, rawDirID)
	if ambiguous {
		// Answering "not found" would hide a real state and answering with one
		// of the matches would be a guess (ADR 0003 §4).
		return nil, &Error{
			ErrKindConflict,
			"DIRECTORY_AMBIGUOUS",
			"more than one folder claims this id; reload the overview",
		}
	}
	if !found {
		return nil, &Error{ErrKindNotFound, "DIRECTORY_NOT_FOUND", "the folder no longer exists"}
	}
	abs, err := s.resolveMember(lib.RootPath, rel)
	if err != nil {
		return nil, err
	}
	return &Member{
		Library: lib,
		RelPath: rel,
		AbsPath: abs,
		DirID:   DirID(lib.ID, lib.RootPath, rel),
	}, nil
}

// Tree reads and assembles the stored tree of one resolved member, so a caller
// that only knows the identity learns what the inventory holds for the path it
// names (ADR 0003 §4).
func (s *Service) Tree(member *Member) (*inventory.TreeNode, error) {
	entries, err := s.inventory.ListEntriesUnderPath(member.AbsPath)
	if err != nil {
		return nil, err
	}
	return inventory.BuildMemberTree(member.AbsPath, entries), nil
}
