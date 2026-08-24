package grpc

import (
	"context"
	"database/sql"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"facette.io/natsort"
	pb "github.com/onsei/organizer/backend/internal/gen/onsei/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ListFolders returns immediate subdirectories of parentPath.
func (s *OnseiServer) ListFolders(_ context.Context, req *pb.ListFoldersRequest) (*pb.ListFoldersResponse, error) {
	parentPath := req.ParentPath
	if parentPath == "" {
		// Return drive roots on Windows / "/" on Unix.
		drives, err := listDriveRoots()
		if err != nil {
			return nil, status.Errorf(codes.Internal, "list drives: %v", err)
		}
		return &pb.ListFoldersResponse{Folders: drives}, nil
	}

	entries, err := os.ReadDir(parentPath)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "read dir %q: %v", parentPath, err)
	}

	var folders []string
	for _, e := range entries {
		if e.IsDir() {
			folders = append(folders, filepath.Join(parentPath, e.Name()))
		}
	}

	sort.Slice(folders, func(i, j int) bool {
		leftName := filepath.Base(folders[i])
		rightName := filepath.Base(folders[j])

		if natsort.Compare(leftName, rightName) {
			return true
		}
		if natsort.Compare(rightName, leftName) {
			return false
		}
		return folders[i] < folders[j]
	})

	return &pb.ListFoldersResponse{Folders: folders}, nil
}

// ListFiles returns audio files in folderPath.
func (s *OnseiServer) ListFiles(_ context.Context, req *pb.ListFilesRequest) (*pb.ListFilesResponse, error) {
	folderPath := req.FolderPath
	if folderPath == "" {
		return nil, status.Errorf(codes.InvalidArgument, "folder_path is required")
	}

	allowedExt := map[string]bool{".mp3": true, ".flac": true, ".wav": true, ".m4a": true, ".aac": true, ".ogg": true}
	var result []string

	// walkDFS performs a depth-first, natural-sorted traversal of dir.
	var walkDFS func(dir string) error
	walkDFS = func(dir string) error {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return err
		}

		var fileNames []string
		var subDirs []string
		for _, e := range entries {
			if e.IsDir() {
				subDirs = append(subDirs, e.Name())
			} else {
				ext := strings.ToLower(filepath.Ext(e.Name()))
				if allowedExt[ext] {
					fileNames = append(fileNames, e.Name())
				}
			}
		}

		sort.Slice(fileNames, func(i, j int) bool {
			if natsort.Compare(fileNames[i], fileNames[j]) {
				return true
			}
			if natsort.Compare(fileNames[j], fileNames[i]) {
				return false
			}
			return fileNames[i] < fileNames[j]
		})
		sort.Slice(subDirs, func(i, j int) bool {
			if natsort.Compare(subDirs[i], subDirs[j]) {
				return true
			}
			if natsort.Compare(subDirs[j], subDirs[i]) {
				return false
			}
			return subDirs[i] < subDirs[j]
		})

		for _, name := range fileNames {
			result = append(result, filepath.Join(dir, name))
		}
		for _, name := range subDirs {
			if err := walkDFS(filepath.Join(dir, name)); err != nil {
				return err
			}
		}
		return nil
	}

	if err := walkDFS(folderPath); err != nil {
		return nil, status.Errorf(codes.NotFound, "read dir %q: %v", folderPath, err)
	}

	// Populate entries with path + bitrate (bps) metadata.
	// Bitrate lookups are only needed for .mp3 files; other formats
	// default to 0 without touching the DB.
	entries := make([]*pb.FileListEntry, 0, len(result))
	for _, p := range result {
		var bitrate int32
		if strings.EqualFold(filepath.Ext(p), ".mp3") {
			var err error
			bitrate, err = s.lookupBitrate(p)
			if err != nil {
				return nil, status.Errorf(codes.Internal, "lookup bitrate for %q: %v", p, err)
			}
		}
		entries = append(entries, &pb.FileListEntry{
			Path:    p,
			Bitrate: bitrate,
		})
	}

	return &pb.ListFilesResponse{Files: result, Entries: entries}, nil
}

// lookupBitrate queries the DB for the bitrate (bps) of a single file path.
// Returns 0 if the repo is nil or no matching row exists.
func (s *OnseiServer) lookupBitrate(path string) (int32, error) {
	if s.repo == nil {
		return 0, nil
	}
	normalized := filepath.ToSlash(path)
	var bitrate sql.NullInt32
	err := s.repo.DB().QueryRow(
		"SELECT bitrate FROM entries WHERE path = ?", normalized,
	).Scan(&bitrate)
	if err != nil {
		if err == sql.ErrNoRows {
			return 0, nil
		}
		return 0, err
	}
	if bitrate.Valid {
		return bitrate.Int32, nil
	}
	return 0, nil
}

// listDriveRoots returns available drive letters on Windows, or "/" on Unix.
func listDriveRoots() ([]string, error) {
	if filepath.Separator == '\\' {
		var drives []string
		for c := 'A'; c <= 'Z'; c++ {
			root := string(c) + ":\\"
			if _, err := os.Stat(root); err == nil {
				drives = append(drives, root)
			}
		}
		return drives, nil
	}

	entries, err := os.ReadDir("/")
	if err != nil {
		return []string{"/"}, nil
	}
	var dirs []string
	for _, e := range entries {
		if e.Type()&fs.ModeSymlink == 0 && e.IsDir() {
			dirs = append(dirs, "/"+e.Name())
		}
	}
	return dirs, nil
}
