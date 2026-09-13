package budget

// Activity sums the amount of every non-deleted transaction in categoryID
// during month (§5.3). month is "YYYY-MM"; a transaction's own date is
// "YYYY-MM-DD" (§5.1), so matching compares its first 7 characters.
func Activity(transactions []Transaction, categoryID, month string) int64 {
	var total int64
	for _, t := range transactions {
		if t.DeletedAt != nil || t.CategoryID != categoryID {
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

// ToBudget computes §5.3's "Available to Budget" figure: the total balance
// of on-budget accounts (already filtered and computed by the caller)
// minus everything budgeted across all months to date.
func ToBudget(accountBalances []int64, allBudgetedAcrossAllMonths int64) int64 {
	var total int64
	for _, b := range accountBalances {
		total += b
	}
	return total - allBudgetedAcrossAllMonths
}
