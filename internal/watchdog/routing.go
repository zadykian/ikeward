package watchdog

import (
	"context"
	"errors"
	"slices"

	"github.com/zadykian/ikeward/internal/routing"
	"github.com/zadykian/ikeward/internal/status"
)

// repairs are the parts of ikeward's routing that the watchdog makes again, each where its check
// failed, in the order ikeward routing makes them.
var repairs = []struct {
	check string
	set   func(routing.System) error
}{
	{status.NATCheck, routing.System.SetNAT},
	{status.FloorCheck, routing.System.SetFloors},
	{status.RuleCheck, routing.System.SetRules},
}

// mendRouting makes again each part of the routing whose check of results failed, as ikeward
// routing does, says what it made, and checks again. It returns the results to go on with: those
// it checked again, else results, with the errors of the parts it could not make. A part holds no
// state but its counter, so making it anew loses nothing.
func (s System) mendRouting(ctx context.Context, out *printer, results []status.Result) (
	[]status.Result, error) {
	r := routing.System{Kernel: s.Kernel, NAT: s.NAT, Files: s.Files, Out: out}
	var errs []error
	made := false
	for _, repair := range repairs {
		if !slices.ContainsFunc(results, func(res status.Result) bool {
			return res.Name == repair.check && !res.OK
		}) {
			continue
		}
		if err := repair.set(r); err != nil {
			errs = append(errs, err)
			continue
		}
		made = true
	}
	if made {
		results = s.Checker.Check(ctx)
	}
	return results, errors.Join(errs...)
}

// Write writes b to w, unless a write failed before, as printf does. It never fails itself, so
// that a failed write stops no repair.
func (p *printer) Write(b []byte) (int, error) {
	if p.err == nil {
		_, p.err = p.w.Write(b)
	}
	return len(b), nil
}
