package execute

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMoveToPersistedTarget_RelativePathRejected(t *testing.T) {
	tmp := t.TempDir()
	absSrc := filepath.Join(tmp, "src.mp3")
	absDst := filepath.Join(tmp, "Delete", "src.mp3")

	if err := os.WriteFile(absSrc, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		src  string
		dst  string
	}{
		{name: "relative src", src: "relative/src.mp3", dst: absDst},
		{name: "relative dst", src: absSrc, dst: "relative/dst.mp3"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := moveToPersistedTarget(tt.src, tt.dst)
			if err == nil {
				t.Fatal("expected error for relative path")
			}
		})
	}
}
