//go:build !windows && !darwin

package keepawake

// Keeper is a no-op on non-Windows builds.
type Keeper struct{}

func Start() *Keeper    { return &Keeper{} }
func (k *Keeper) Stop() {}
