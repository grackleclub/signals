// Package signals is OTel-native observability for grackleclub services:
// logs, metrics, and traces bootstrapped from one Setup call, exported over
// OTLP/HTTP to SigNoz as the single source of truth.
//
// This is the TDD test-side skeleton: the public API exists but is stubbed
// (every entry point returns errNotImplemented or nil), so the tests in this
// package fail until the implementation lands. See DESIGN.md for the contract
// and plan.md for what remains.
package signals
