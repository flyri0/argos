package budget

import "sort"

// BudgetEntry is the minimal budget_entries shape (§5.2) the rollup needs.
// Callers pass only non-deleted rows.
type BudgetEntry struct {
	CategoryID string
	Month      string // "YYYY-MM"
	Budgeted   int64
}

// CategoryMonth holds one category's §5.3 figures for a single month.
type CategoryMonth struct {
	Budgeted  int64
	Activity  int64
	Available int64
}

// Activity sums the amount of every non-deleted transaction in categoryID
// during month (§5.3), counting only transactions in on-budget accounts.
// month is "YYYY-MM"; a transaction's own date is "YYYY-MM-DD" (§5.1), so
// matching compares its first 7 characters.
func Activity(transactions []Transaction, categoryID, month string) int64 {
	var total int64
	for _, t := range transactions {
		if t.DeletedAt != nil || !t.OnBudget || t.CategoryID != categoryID {
			continue
		}
		if len(t.Date) < 7 || t.Date[:7] != month {
			continue
		}
		total += t.Amount
	}
	return total
}

// Available implements §5.3's rollover rule: a negative previousAvailable
// (last month overspent) resets to zero rather than carrying the deficit
// forward. Pass previousAvailable as 0 for a category's first budgeted
// month, since there is no prior month to roll over from.
func Available(previousAvailable, budgeted, activity int64) int64 {
	if previousAvailable < 0 {
		previousAvailable = 0
	}
	return previousAvailable + budgeted + activity
}

// RollupCategory walks categoryID's full budget entry/transaction history up
// to and including month, applying Available forward from the earliest
// relevant month, so a rollover or an overspend from arbitrarily far back
// still reaches the target month correctly. entries and transactions may
// include other categories' rows; they're ignored.
func RollupCategory(entries []BudgetEntry, transactions []Transaction, categoryID, month string) CategoryMonth {
	budgetedByMonth := make(map[string]int64)
	months := map[string]bool{month: true}
	for _, e := range entries {
		if e.CategoryID != categoryID {
			continue
		}
		budgetedByMonth[e.Month] = e.Budgeted
		months[e.Month] = true
	}
	for _, t := range transactions {
		if t.CategoryID != categoryID || len(t.Date) < 7 {
			continue
		}
		months[t.Date[:7]] = true
	}

	ordered := make([]string, 0, len(months))
	for m := range months {
		if m <= month {
			ordered = append(ordered, m)
		}
	}
	sort.Strings(ordered)

	var previousAvailable int64
	var out CategoryMonth
	for _, m := range ordered {
		budgeted := budgetedByMonth[m]
		activity := Activity(transactions, categoryID, m)
		available := Available(previousAvailable, budgeted, activity)
		previousAvailable = available
		if m == month {
			out = CategoryMonth{Budgeted: budgeted, Activity: activity, Available: available}
		}
	}
	return out
}

// BalanceThrough sums every non-deleted transaction in accountID dated on or
// before the last day of month (§5.3). "YYYY-MM-DD" dates order
// lexicographically, so comparing the month prefix is enough.
func BalanceThrough(accountID, month string, transactions []Transaction) int64 {
	var total int64
	for _, t := range transactions {
		if t.AccountID != accountID || t.DeletedAt != nil {
			continue
		}
		if len(t.Date) < 7 || t.Date[:7] > month {
			continue
		}
		total += t.Amount
	}
	return total
}

// ToBudget computes §5.3's "Available to Budget": on-budget balances through
// the month minus what is still sitting in non-income envelopes. Subtracting
// available rather than budgeted is what keeps categorized spending from
// being counted twice — it's already in both the balance and the envelope.
func ToBudget(onBudgetBalancesThrough []int64, nonIncomeAvailable []int64) int64 {
	var total int64
	for _, b := range onBudgetBalancesThrough {
		total += b
	}
	for _, a := range nonIncomeAvailable {
		total -= a
	}
	return total
}
