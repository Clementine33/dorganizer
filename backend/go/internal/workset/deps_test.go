package workset_test

import (
	"os/exec"
	"strings"
	"testing"
)

// TestWorksetHasNoTaskDependencies pins the task seam: the generic workset
// module must not depend on any task package or on the media services that
// belong to a task. Everything business-specific crosses the seam.
func TestWorksetHasNoTaskDependencies(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", ".").Output()
	if err != nil {
		t.Fatalf("go list -deps: %v", err)
	}
	for _, banned := range []string{
		"internal/tasks/",
		"internal/services/reconcile",
		"internal/services/execute",
		"internal/usecase/plan",
	} {
		if strings.Contains(string(out), banned) {
			t.Fatalf("workset depends on %s;%s", banned, firstMatch(out, banned))
		}
	}
}

// firstMatch renders the offending dependency lines for the failure message.
func firstMatch(out []byte, banned string) string {
	var hits []string
	for line := range strings.SplitSeq(string(out), "\n") {
		if strings.Contains(line, banned) {
			hits = append(hits, line)
		}
	}
	if len(hits) == 0 {
		return ""
	}
	return " offending: " + strings.Join(hits, ", ")
}
