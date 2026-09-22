package execute

import (
	"context"

	"github.com/onsei/organizer/backend/internal/conversion/reconcile"
)

// ComponentRunTestKit replaces component-run IO in external tests so failures
// can be injected at specific filesystem boundaries; a nil field keeps the
// real implementation.
type ComponentRunTestKit struct {
	Encode func(ctx context.Context, src, dst string, spec reconcile.AudioOutputSpec) error
	Rename func(oldpath, newpath string) error
	Remove func(path string) error
}

// PrepareComponentWithTestKit prepares one component with replaced IO, the
// shape a concurrent session drives.
func PrepareComponentWithTestKit(
	ctx context.Context, req ComponentRunRequest, kit ComponentRunTestKit,
) (*PreparedComponent, error) {
	return prepareComponent(ctx, req, testToolkit(req.Encoder, kit))
}

// RunComponentWithTestKit runs the exported component entry with replaced IO.
func RunComponentWithTestKit(
	ctx context.Context, req ComponentRunRequest, kit ComponentRunTestKit,
) (ComponentRunResult, error) {
	return runComponent(ctx, req, testToolkit(req.Encoder, kit))
}

// testToolkit is the component IO surface of one test kit.
func testToolkit(encoder Encoder, kit ComponentRunTestKit) *componentToolkit {
	tk := defaultComponentToolkit(encoder)
	if kit.Encode != nil {
		tk.encode = kit.Encode
	}
	if kit.Rename != nil {
		tk.rename = kit.Rename
	}
	if kit.Remove != nil {
		tk.remove = kit.Remove
	}
	return tk
}
