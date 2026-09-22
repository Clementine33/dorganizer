package arch_test

import (
	"os/exec"
	"strings"
	"testing"
)

// The layering rules of ADR 0009, as dependencies rather than as intentions.
//
// A rule is checked with go list -deps, which lists a package's transitive
// closure and — without -test — only its production edges: a package's own
// tests may import the real adapter they need to be honest, while the package
// itself may not. The exception is a rule about a package's own files, where
// only its direct imports carry the meaning.
type rule struct {
	// name is what the rule is, in the words a reviewer would use.
	name string
	// pkg is the package the rule constrains.
	pkg string
	// banned are import paths that must not appear among the package's edges.
	banned []string
	// direct judges the package's own import lines instead of its closure.
	direct bool
	// why explains what the rule buys, for the failure message.
	why string
}

const module = "github.com/onsei/organizer/backend/internal"

var rules = []rule{
	{
		name:   "business packages reach storage and tools through ports",
		pkg:    module + "/library",
		banned: []string{module + "/adapters"},
		why:    "a business package that names an adapter cannot be tested or reused without it",
	},
	{
		name:   "business packages reach storage and tools through ports",
		pkg:    module + "/inventory",
		banned: []string{module + "/adapters"},
		why:    "the scan pipeline is injected with its walker and its staging store",
	},
	{
		name:   "business packages reach storage and tools through ports",
		pkg:    module + "/workset",
		banned: []string{module + "/adapters"},
		why:    "the record types are the workset's own; Store is what it asks for",
	},
	{
		name:   "business packages reach storage and tools through ports",
		pkg:    module + "/conversion",
		banned: []string{module + "/adapters"},
		why:    "the task reads the inventory and the configuration through its own ports",
	},
	{
		name:   "business packages reach storage and tools through ports",
		pkg:    module + "/conversion/execute",
		banned: []string{module + "/adapters"},
		why:    "the encoder is a port: the execution rules never name ffmpeg",
	},
	{
		name:   "business packages reach storage and tools through ports",
		pkg:    module + "/fileops",
		banned: []string{module + "/adapters"},
		why:    "direct file management takes its inventory refresh through a seam",
	},
	{
		name:   "the transport does not talk to the database",
		pkg:    module + "/adapters/httpapi",
		banned: []string{module + "/adapters/sqlite"},
		why:    "every route reaches a business entry; only that entry knows the schema",
	},
	{
		name:   "the generic side does not know the task",
		pkg:    module + "/workset",
		banned: []string{module + "/conversion"},
		why:    "the conversion task implements the workset's seam, never the other way round",
	},
	{
		name:   "business packages do not know the process",
		pkg:    module + "/library",
		banned: []string{module + "/app"},
		why:    "assembly depends on the modules, never the reverse",
	},
	{
		name:   "business packages do not know the process",
		pkg:    module + "/inventory",
		banned: []string{module + "/app"},
		why:    "a module that could start the process can no longer be driven by a caller",
	},
	{
		name:   "business packages do not know the process",
		pkg:    module + "/workset",
		banned: []string{module + "/app"},
		why:    "the session layer is handed its seams by the assembly root",
	},
	{
		name:   "business packages do not know the process",
		pkg:    module + "/conversion",
		banned: []string{module + "/app"},
		why:    "the task is constructed by the assembly root and knows nothing of it",
	},
	{
		name:   "business packages do not know the process",
		pkg:    module + "/fileops",
		banned: []string{module + "/app"},
		why:    "file management is driven by its callers, not by the process",
	},
	{
		// The process itself is judged on the package's own imports: `os` is a
		// transitive dependency of almost everything (fmt reaches it), so only
		// a package's own files say whether it touches the process.
		name:   "the planning rules stay pure",
		pkg:    module + "/conversion/reconcile",
		banned: []string{"os", "os/exec", "database/sql", "net/http"},
		direct: true,
		why:    "reconciliation decides what should be on disk; it may not touch it",
	},
	{
		// The rest have no innocent transitive path in: reaching one from here
		// means the planning rules grew a dependency that does I/O.
		name:   "the planning rules stay pure",
		pkg:    module + "/conversion/reconcile",
		banned: []string{"os/exec", "database/sql", "net/http"},
		why:    "and not through a dependency either, since none of these arrives on its own",
	},
}

// TestDependenciesFollowTheLayeringRules walks the table. Every rule is checked
// against the production dependency graph, so a rule is violated exactly when
// the shipped code violates it.
func TestDependenciesFollowTheLayeringRules(t *testing.T) {
	for _, tc := range rules {
		t.Run(tc.pkg+"/"+strings.Join(tc.banned, ","), func(t *testing.T) {
			edges := deps(t, tc.pkg, tc.direct)
			for _, banned := range tc.banned {
				if hit := match(edges, banned); hit != "" {
					t.Fatalf("%s depends on %s (%s): %s\n%s", tc.pkg, banned, tc.why, hit, tc.name)
				}
			}
		})
	}
}

// deps lists what a package depends on: its transitive closure, or — for a rule
// about the package's own files — its direct imports.
func deps(t *testing.T, pkg string, direct bool) []string {
	t.Helper()
	args := []string{"list", "-deps", pkg}
	if direct {
		args = []string{"list", "-f", `{{join .Imports "\n"}}`, pkg}
	}
	out, err := exec.Command("go", args...).Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	return strings.Split(string(out), "\n")
}

// match reports the first edge naming the banned path, rendering it for the
// failure message.
func match(edges []string, banned string) string {
	for _, edge := range edges {
		if edge == banned {
			return edge
		}
	}
	return ""
}
