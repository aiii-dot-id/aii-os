package store

import "strings"

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
const (
	EvidenceNotRun           = "not_run"
	EvidenceRejectedNoEffect = "rejected_before_effect"
	EvidenceCompletedLocally = "completed_locally"
	EvidencePartialOrMixed   = "partial_or_mixed"
	EvidenceExternalUnknown  = "external_effect_unknown"
	EvidenceWorkerReportOnly = "worker_report_only"
	EvidenceLocallyVerified  = "locally_verified"
	EvidenceHostReceipted    = "host_receipted"
)

// .
var evidenceClasses = []string{
	EvidenceNotRun,
	EvidenceRejectedNoEffect,
	EvidenceCompletedLocally,
	EvidencePartialOrMixed,
	EvidenceExternalUnknown,
	EvidenceWorkerReportOnly,
	EvidenceLocallyVerified,
	EvidenceHostReceipted,
}

// .
// .
func IsEvidenceClass(s string) bool {
	for _, c := range evidenceClasses {
		if s == c {
			return true
		}
	}
	return false
}

// .
// .
// .
// .
// .
func EvidenceVerifiedTier(s string) bool {
	return s == EvidenceLocallyVerified || s == EvidenceHostReceipted
}

// .
// .
// .
// .
// .
func EvidenceScopeLabel(class string) string {
	switch class {
	case EvidenceNotRun:
		return "not attempted"
	case EvidenceRejectedNoEffect:
		return "rejected before effect"
	case EvidenceCompletedLocally:
		return "locally observed"
	case EvidencePartialOrMixed:
		return "partial / mixed"
	case EvidenceExternalUnknown:
		return "external effect unknown"
	case EvidenceWorkerReportOnly:
		return "worker-reported"
	case EvidenceLocallyVerified:
		return "locally verified"
	case EvidenceHostReceipted:
		return "host receipted"
	default:
		return ""
	}
}

// .
// .
// .
// .
// .
// .
func EvidenceRecovery(class string) string {
	switch class {
	case EvidenceRejectedNoEffect:
		return "correct the named contract, then retry only after reading the correction back"
	case EvidenceExternalUnknown:
		return "inspect the external state / idempotency key before retrying — do not retry blind"
	case EvidenceWorkerReportOnly:
		return "verify from the primary seat: run the smallest check yourself and read it back"
	case EvidencePartialOrMixed:
		return "inspect the durable and external boundaries separately; do not retry blindly"
	default:
		// .
		// .
		return ""
	}
}

// .
// .
// .
// .
// .
// .
func ambiguousEvidenceClasses() []string {
	var out []string
	for _, c := range evidenceClasses {
		if EvidenceRecovery(c) != "" {
			out = append(out, c)
		}
	}
	return out
}

// .
// .
// .
// .
func RenderEvidenceScope(class string) string {
	label := EvidenceScopeLabel(class)
	if label == "" {
		return ""
	}
	if rec := EvidenceRecovery(class); rec != "" {
		return "[" + label + " — recovery: " + rec + "]"
	}
	return "[" + label + "]"
}

// .
func evidenceReadbackClean(s string) string { return strings.TrimSpace(s) }
