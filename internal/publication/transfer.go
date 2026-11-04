package publication

import "time"

// TransferBudgeter is implemented by backends whose object transfers have a
// size-aware worst-case duration. Callers use it to size the context of a step
// that moves several objects; the backend still enforces each object itself.
// Backends that do not implement it (local, test fakes) add no allowance.
type TransferBudgeter interface {
	// TransferBudget bounds one PutFile of an encoded object of size bytes,
	// including the read-back verification after a lost acknowledgement.
	TransferBudget(size int64) time.Duration
}
