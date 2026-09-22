// This test is in the package (not workset_test) on purpose: it pins the
// sealing rule of the pool's own tracker, which the exported seam cannot reach.
package workset

import (
	"context"
	"testing"
)

// TestOpenUnitSealsOnlyAfterDeliveryEnded pins the sealing rule directly: the
// first task of two returning while the second is still undelivered must
// neither seal the unit nor make it committable, and delivery ending with a
// task outstanding must not make it committable either.
func TestOpenUnitSealsOnlyAfterDeliveryEnded(t *testing.T) {
	unit := newOpenUnit(sealingUnit{tasks: 2}, ExecutionUnit{ID: "u0"}, 0)
	unit.taskDelivered()
	unit.taskReturned(nil) // delivered == completed, but one task is undelivered

	select {
	case <-unit.done:
		t.Fatal("a unit must not seal while one of its tasks was never delivered")
	default:
	}
	if unit.committable() {
		t.Fatal("delivered == completed must not make a unit committable")
	}

	unit.taskDelivered()
	unit.endDelivery() // delivery ended with the second task still outstanding
	if unit.committable() {
		t.Fatal("an outstanding task must not commit")
	}
	unit.taskReturned(nil)
	if !unit.committable() {
		t.Fatal("a fully delivered, fully returned unit must commit")
	}
	<-unit.done
}

// sealingUnit is a prepared unit with a fixed task count and nothing to do.
type sealingUnit struct{ tasks int }

func (u sealingUnit) EncodeTasks() int { return u.tasks }

func (sealingUnit) EncodeTask(context.Context, int) error { return nil }

func (sealingUnit) Commit(context.Context) (UnitResult, error) { return UnitResult{}, nil }

func (sealingUnit) Discard(error) UnitResult { return UnitResult{} }
