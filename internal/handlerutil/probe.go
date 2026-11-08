package handlerutil

import "context"

type probeContextKey struct{}

var probeContextKeyVal = probeContextKey{}

// WithProbeContext marks a context as belonging to a model test probe.
func WithProbeContext(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, probeContextKeyVal, true)
}

// IsProbeContext reports whether the context belongs to a model test probe.
func IsProbeContext(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	v, _ := ctx.Value(probeContextKeyVal).(bool)
	return v
}

type banCheckContextKey struct{}

var banCheckContextKeyVal = banCheckContextKey{}

// WithBanCheckContext marks a context as the operator's ban check: one named
// account, asked whether an appeal has been granted yet.
//
// A distinct marker rather than a wider IsProbeContext, because the probe
// marker is shared with the model-health scheduler. A ban check has to reach a
// connection the operator switched off — that is the whole point, since a
// banned account is normally left disabled — and granting that to every probe
// would have the scheduler quietly exercising accounts that were deliberately
// parked.
//
// It implies the probe marker: a ban check is also a single-request probe, so
// every relaxation a probe already earns (the suspension halt is not applied,
// no deprecation badge is recorded) applies to it too.
func WithBanCheckContext(ctx context.Context) context.Context {
	return context.WithValue(WithProbeContext(ctx), banCheckContextKeyVal, true)
}

// IsBanCheckContext reports whether the context belongs to an operator ban
// check.
func IsBanCheckContext(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	v, _ := ctx.Value(banCheckContextKeyVal).(bool)
	return v
}
