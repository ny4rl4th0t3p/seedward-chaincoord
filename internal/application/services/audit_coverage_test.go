package services

import (
	"reflect"
	"testing"
)

// Audit-coverage guard. Every exported method of the audit-relevant services must be classified
// below as either emitting an audit event (true) or not (false — a query, a With* builder, or a
// mutation intentionally left unaudited with the reason noted). A new exported method absent from
// the map fails TestAuditCoverage_* — forcing an explicit audit-or-not decision so this class of
// gap (a mutating method that silently skips the audit log) cannot reopen. It does not prove the
// audit fires — the per-method _Audited tests do that — it prevents a new mutation from escaping
// the decision entirely.

var launchServiceAuditCoverage = map[string]bool{
	// Audited mutations.
	"CreateLaunch":              true,
	"PatchLaunch":               true,
	"OpenWindow":                true,
	"CancelLaunch":              true,
	"AddMember":                 true,
	"RemoveMember":              true,
	"SetCommittee":              true,
	"UploadInitialGenesis":      true,
	"UploadInitialGenesisRef":   true,
	"UploadFinalGenesis":        true,
	"UploadFinalGenesisRef":     true,
	"UploadAllocationFileBytes": true,
	"UploadAllocationFileRef":   true,
	"RecordRehearsalResult":     true,
	"ResetRehearsalAttempt":     true,

	// Read-only queries.
	"GetLaunch":             false,
	"GetChainHint":          false,
	"GetCommittee":          false,
	"GetDashboard":          false,
	"IsCommitteeMember":     false,
	"ListLaunches":          false,
	"ListMembers":           false,
	"ListRehearsalResults":  false,
	"PreviewRehearsalInput": false,

	// Builder options (construction-time, no mutation).
	"WithLogger":            false,
	"WithRehearsalLeaseTTL": false,
	"WithURLValidator":      false,

	// Intentionally unaudited mutation: claiming a rehearsal-run lease is high-frequency bridge
	// protocol, not a governance/security action — the result it produces (RecordRehearsalResult)
	// is what gets audited. A manual force-release (ResetRehearsalAttempt) is audited.
	"ClaimRehearsalRun": false,
}

var proposalServiceAuditCoverage = map[string]bool{
	"Raise": true, // an auto-executing (quorum-reached) proposal audits via applyAndSave
	"Sign":  true, // execution on quorum audits via applyAndSave/dispatchEvents

	"ExpireStale":   false, // GC of proposals that never executed — no governance action occurred
	"GetByID":       false,
	"ListForLaunch": false,

	"WithLogger":        false,
	"WithRehearsalGate": false,
}

func TestAuditCoverage_LaunchServiceClassified(t *testing.T) {
	assertMethodsClassified(t, reflect.TypeOf((*LaunchService)(nil)), launchServiceAuditCoverage)
}

func TestAuditCoverage_ProposalServiceClassified(t *testing.T) {
	assertMethodsClassified(t, reflect.TypeOf((*ProposalService)(nil)), proposalServiceAuditCoverage)
}

// assertMethodsClassified checks the coverage map and the type's exported method set are exactly in
// sync: every method is classified (so a new mutation cannot escape the decision), and every entry
// names a real method (so a stale entry left after a rename/removal is caught too).
func assertMethodsClassified(t *testing.T, typ reflect.Type, coverage map[string]bool) {
	t.Helper()
	svc := typ.Elem().Name()
	methods := make(map[string]bool, typ.NumMethod())
	for i := range typ.NumMethod() {
		name := typ.Method(i).Name
		methods[name] = true
		if _, ok := coverage[name]; !ok {
			t.Errorf("%s.%s is not classified in the audit-coverage map — add it as audited (true) or "+
				"explain why not (false: query, builder, or an intentionally-unaudited mutation)", svc, name)
		}
	}
	for name := range coverage {
		if !methods[name] {
			t.Errorf("audit-coverage map lists %s.%s, which is not an exported method — remove the stale entry", svc, name)
		}
	}
}
