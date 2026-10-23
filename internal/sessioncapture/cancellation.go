package sessioncapture

import "context"

// IsCallerCancellation reports whether every error-tree leaf is the caller's
// cancellation sentinel. It never treats joined durability/integrity failures as
// successful shutdown merely because another branch contains context.Canceled.
func IsCallerCancellation(ctx context.Context, err error) bool {
	return ctx.Err() != nil && pureCancellation(err, ctx.Err())
}
