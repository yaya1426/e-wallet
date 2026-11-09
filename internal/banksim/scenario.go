package banksim

import "strings"

// Scenarios (TDR-008 §3, phase-1 subset). Any other IBAN suffix behaves like ScenarioSuccess.
const (
	ScenarioSuccess        = "0000" // 202, later SUCCEEDED + webhook
	ScenarioFailLater      = "0001" // 202, later FAILED + webhook
	ScenarioInvalidAccount = "0002" // 422 INVALID_ACCOUNT now, no operation
)

func validScenario(s string) bool {
	return s == ScenarioSuccess || s == ScenarioFailLater || s == ScenarioInvalidAccount
}

// pickScenario returns the scenario for the next request on iban: a pending one-shot
// override (consumed here), else the IBAN's last four characters. The caller holds s.mu.
func (s *store) pickScenario(iban string) string {
	if sc, ok := s.overrides[iban]; ok {
		delete(s.overrides, iban)
		return sc
	}
	if suffix := iban[max(0, len(iban)-4):]; validScenario(suffix) {
		return suffix
	}
	return ScenarioSuccess
}

// failure is the business reason of ScenarioFailLater per kind.
func failure(kind string) (code, message string) {
	if strings.EqualFold(kind, kindPayout) {
		return "ACCOUNT_CLOSED", "The destination account is closed."
	}
	return "INSUFFICIENT_FUNDS", "Insufficient funds in the source account."
}
