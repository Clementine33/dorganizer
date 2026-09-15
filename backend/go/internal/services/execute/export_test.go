package execute

import (
	"context"

	"github.com/onsei/organizer/backend/internal/services/reconcile"
)

// ComponentRunTestKit replaces component-run IO in external tests so failures
// can be injected at specific filesystem boundaries; a nil field keeps the
// real implementation.
type ComponentRunTestKit struct {
	Encode func(ctx context.Context, src, dst string, spec reconcile.AudioOutputSpec) error
	Rename func(oldpath, newpath string) error
	Remove func(path string) error
}

// RunComponentWithTestKit runs the exported component entry with replaced IO.
func RunComponentWithTestKit(
	ctx context.Context, req ComponentRunRequest, kit ComponentRunTestKit,
) (ComponentRunResult, error) {
	tk := defaultComponentToolkit(req.Tools)
	if kit.Encode != nil {
		tk.encode = kit.Encode
	}
	if kit.Rename != nil {
		tk.rename = kit.Rename
	}
	if kit.Remove != nil {
		tk.remove = kit.Remove
	}
	return runComponent(ctx, req, tk)
}
