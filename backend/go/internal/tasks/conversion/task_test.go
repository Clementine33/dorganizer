package conversion_test

import (
	"testing"

	"github.com/onsei/organizer/backend/internal/services/execute"
	worksetusecase "github.com/onsei/organizer/backend/internal/usecase/workset"
)

// TestDeleteModeConstantsAgree pins the bridge between the two constant sets:
// the generic side freezes the session option string, the execute service owns
// the file behavior, and the conversion task maps one onto the other. If the
// values ever drift apart, the mapping silently degrades — so assert it here.
func TestDeleteModeConstantsAgree(t *testing.T) {
	if string(execute.DeleteModeSoft) != worksetusecase.ExecutionDeleteModeSoft {
		t.Fatalf(
			"soft mode: execute=%q session option=%q",
			execute.DeleteModeSoft,
			worksetusecase.ExecutionDeleteModeSoft,
		)
	}
	if string(execute.DeleteModeHard) != worksetusecase.ExecutionDeleteModeHard {
		t.Fatalf(
			"hard mode: execute=%q session option=%q",
			execute.DeleteModeHard,
			worksetusecase.ExecutionDeleteModeHard,
		)
	}
}
