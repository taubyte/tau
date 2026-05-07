package projectLib

import "fmt"

// BindingFlags enforces the both-or-neither rule for --account / --plan.
// Half-set fails with an explicit error rather than silently dropping one
// half. Pure so the rule is unit-testable without a CLI fixture.
func BindingFlags(account, plan string) (string, string, error) {
	if (account == "") != (plan == "") {
		return "", "", fmt.Errorf("--account and --plan must be set together (got --account=%q --plan=%q)", account, plan)
	}
	return account, plan, nil
}
