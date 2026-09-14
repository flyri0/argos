// Package budget holds the budgeting engine's pure business logic (§5.3):
// rollover, overspending, and derived values like balance and available.
// It has zero dependency on internal/api or the DB layer so it can be
// unit tested in isolation.
package budget

// Transaction is the minimal shape balance and envelope calculations need.
// It's deliberately independent of internal/db's row type to keep this
// package free of any DB-layer dependency.
type Transaction struct {
	AccountID  string
	CategoryID string
	Date       string // "YYYY-MM-DD" (§5.1)
	Amount     int64
	DeletedAt  *int64
	// OnBudget is whether the transaction's account is on-budget and
	// non-deleted; only such transactions count toward activity (§5.3).
	OnBudget bool
}

// AccountBalance sums the amount of every non-deleted transaction belonging
// to accountID. Per §5.3, balance is always computed this way and never
// stored as its own column.
func AccountBalance(accountID string, transactions []Transaction) int64 {
	var total int64
	for _, t := range transactions {
		if t.AccountID != accountID || t.DeletedAt != nil {
			continue
		}
		total += t.Amount
	}
	return total
}
