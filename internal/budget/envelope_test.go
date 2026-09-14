package budget

import "testing"

func TestAvailable_PositiveRollover(t *testing.T) {
	// Last month ended with 5000 left over; this month budgets 2000 more
	// and has 1000 of spending (activity is negative).
	got := Available(5000, 2000, -1000)
	want := int64(6000)
	if got != want {
		t.Fatalf("expected %d, got %d", want, got)
	}
}

func TestAvailable_ResetsAfterOverspending(t *testing.T) {
	// Last month overspent by 3000; the negative must NOT carry forward.
	got := Available(-3000, 1000, -500)
	want := int64(500) // 0 (reset) + 1000 - 500
	if got != want {
		t.Fatalf("expected %d (negative rollover reset to 0), got %d", want, got)
	}
}

func TestAvailable_FirstMonthBudgeted(t *testing.T) {
	// No prior month at all: caller passes previousAvailable as 0.
	got := Available(0, 1000, -200)
	want := int64(800)
	if got != want {
		t.Fatalf("expected %d, got %d", want, got)
	}
}

func TestActivity_IgnoresOffBudgetDeletedOtherCategoriesAndMonths(t *testing.T) {
	transactions := []Transaction{
		{CategoryID: "cat-1", Date: "2026-03-05", Amount: -1000, OnBudget: true},
		{CategoryID: "cat-1", Date: "2026-03-20", Amount: -500, OnBudget: true},
		{CategoryID: "cat-1", Date: "2026-03-06", Amount: -9000, OnBudget: false},
		{CategoryID: "cat-2", Date: "2026-03-05", Amount: -9000, OnBudget: true},
		{CategoryID: "cat-1", Date: "2026-04-05", Amount: -9000, OnBudget: true},
		{CategoryID: "cat-1", Date: "2026-03-05", Amount: -9000, OnBudget: true, DeletedAt: ptr(1)},
	}
	if got := Activity(transactions, "cat-1", "2026-03"); got != -1500 {
		t.Fatalf("expected -1500, got %d", got)
	}
}

func TestRollupCategory(t *testing.T) {
	cases := []struct {
		name         string
		entries      []BudgetEntry
		transactions []Transaction
		month        string
		want         CategoryMonth
	}{
		{
			name:         "single month with no history",
			entries:      []BudgetEntry{{CategoryID: "cat-1", Month: "2026-03", Budgeted: 5000}},
			transactions: []Transaction{{CategoryID: "cat-1", Date: "2026-03-10", Amount: -2000, OnBudget: true}},
			month:        "2026-03",
			want:         CategoryMonth{Budgeted: 5000, Activity: -2000, Available: 3000},
		},
		{
			// February and March have no entry and no activity; January's
			// leftover must still be there in March.
			name:         "positive available carries across empty months",
			entries:      []BudgetEntry{{CategoryID: "cat-1", Month: "2026-01", Budgeted: 10000}},
			transactions: []Transaction{{CategoryID: "cat-1", Date: "2026-01-15", Amount: -4000, OnBudget: true}},
			month:        "2026-03",
			want:         CategoryMonth{Available: 6000},
		},
		{
			name: "overspent month resets to zero going into the next",
			entries: []BudgetEntry{
				{CategoryID: "cat-1", Month: "2026-01", Budgeted: 1000},
				{CategoryID: "cat-1", Month: "2026-02", Budgeted: 500},
			},
			transactions: []Transaction{{CategoryID: "cat-1", Date: "2026-01-20", Amount: -3000, OnBudget: true}},
			month:        "2026-02",
			want:         CategoryMonth{Budgeted: 500, Available: 500},
		},
		{
			name: "ignores other categories",
			entries: []BudgetEntry{
				{CategoryID: "cat-1", Month: "2026-03", Budgeted: 1000},
				{CategoryID: "cat-2", Month: "2026-03", Budgeted: 9000},
			},
			transactions: []Transaction{
				{CategoryID: "cat-1", Date: "2026-03-05", Amount: -100, OnBudget: true},
				{CategoryID: "cat-2", Date: "2026-03-05", Amount: -9999, OnBudget: true},
			},
			month: "2026-03",
			want:  CategoryMonth{Budgeted: 1000, Activity: -100, Available: 900},
		},
		{
			name: "ignores entries and transactions after the month",
			entries: []BudgetEntry{
				{CategoryID: "cat-1", Month: "2026-03", Budgeted: 1000},
				{CategoryID: "cat-1", Month: "2026-04", Budgeted: 999999},
			},
			transactions: []Transaction{{CategoryID: "cat-1", Date: "2026-04-01", Amount: -999999, OnBudget: true}},
			month:        "2026-03",
			want:         CategoryMonth{Budgeted: 1000, Available: 1000},
		},
		{
			name:         "ignores off-budget transactions",
			entries:      []BudgetEntry{{CategoryID: "cat-1", Month: "2026-03", Budgeted: 1000}},
			transactions: []Transaction{{CategoryID: "cat-1", Date: "2026-03-05", Amount: -700, OnBudget: false}},
			month:        "2026-03",
			want:         CategoryMonth{Budgeted: 1000, Available: 1000},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := RollupCategory(tc.entries, tc.transactions, "cat-1", tc.month)
			if got != tc.want {
				t.Fatalf("expected %+v, got %+v", tc.want, got)
			}
		})
	}
}

func TestBalanceThrough(t *testing.T) {
	transactions := []Transaction{
		{AccountID: "acc-1", Date: "2026-02-28", Amount: 1000},
		{AccountID: "acc-1", Date: "2026-03-01", Amount: 200},
		{AccountID: "acc-1", Date: "2026-03-31", Amount: 30},
		{AccountID: "acc-1", Date: "2026-04-01", Amount: 99999},                    // after the month
		{AccountID: "acc-1", Date: "2026-03-15", Amount: 99999, DeletedAt: ptr(1)}, // deleted
		{AccountID: "acc-2", Date: "2026-03-15", Amount: 99999},                    // other account
	}

	if got := BalanceThrough("acc-1", "2026-03", transactions); got != 1230 {
		t.Fatalf("expected 1230 through 2026-03, got %d", got)
	}
	if got := BalanceThrough("acc-1", "2026-01", transactions); got != 0 {
		t.Fatalf("expected 0 through 2026-01, got %d", got)
	}
}

type invariantAccount struct {
	id       string
	onBudget bool
}

// evaluateMonth reproduces what internal/api's budget handler does with the
// engine: roll up every category, subtract only non-income available, and
// use balances through the month.
func evaluateMonth(accounts []invariantAccount, categories []string, income map[string]bool,
	entries []BudgetEntry, transactions []Transaction, month string,
) (toBudget, balanceSum, nonIncomeAvailableSum int64, available map[string]int64) {
	onBudget := make(map[string]bool)
	for _, a := range accounts {
		onBudget[a.id] = a.onBudget
	}
	txns := make([]Transaction, len(transactions))
	for i, t := range transactions {
		t.OnBudget = onBudget[t.AccountID]
		txns[i] = t
	}

	available = make(map[string]int64)
	var nonIncome []int64
	for _, c := range categories {
		a := RollupCategory(entries, txns, c, month).Available
		available[c] = a
		if !income[c] {
			nonIncome = append(nonIncome, a)
			nonIncomeAvailableSum += a
		}
	}

	var balances []int64
	for _, a := range accounts {
		if !a.onBudget {
			continue
		}
		b := BalanceThrough(a.id, month, txns)
		balances = append(balances, b)
		balanceSum += b
	}

	return ToBudget(balances, nonIncome), balanceSum, nonIncomeAvailableSum, available
}

func TestToBudgetInvariant(t *testing.T) {
	checking := invariantAccount{id: "checking", onBudget: true}
	investment := invariantAccount{id: "investment", onBudget: false}
	paycheck := Transaction{AccountID: "checking", Date: "2026-09-01", Amount: 1000}
	groceriesSep := BudgetEntry{CategoryID: "groceries", Month: "2026-09", Budgeted: 100}

	cases := []struct {
		name          string
		accounts      []invariantAccount
		categories    []string
		income        map[string]bool
		entries       []BudgetEntry
		transactions  []Transaction
		month         string
		wantToBudget  int64
		wantAvailable map[string]int64
	}{
		{
			// Balance 940, Groceries 40: the 60 spent must not be subtracted
			// a second time on top of the envelope.
			name:       "categorized spending under budget",
			accounts:   []invariantAccount{checking},
			categories: []string{"groceries"},
			entries:    []BudgetEntry{groceriesSep},
			transactions: []Transaction{
				paycheck,
				{AccountID: "checking", CategoryID: "groceries", Date: "2026-09-10", Amount: -60},
			},
			month:         "2026-09",
			wantToBudget:  900,
			wantAvailable: map[string]int64{"groceries": 40},
		},
		{
			name:       "overspending in September",
			accounts:   []invariantAccount{checking},
			categories: []string{"groceries"},
			entries:    []BudgetEntry{groceriesSep},
			transactions: []Transaction{
				paycheck,
				{AccountID: "checking", CategoryID: "groceries", Date: "2026-09-10", Amount: -150},
			},
			month:         "2026-09",
			wantToBudget:  900,
			wantAvailable: map[string]int64{"groceries": -50},
		},
		{
			// The overspend resets Groceries to 0, so the 50 now comes out
			// of to_budget instead.
			name:       "October after September overspending",
			accounts:   []invariantAccount{checking},
			categories: []string{"groceries"},
			entries:    []BudgetEntry{groceriesSep},
			transactions: []Transaction{
				paycheck,
				{AccountID: "checking", CategoryID: "groceries", Date: "2026-09-10", Amount: -150},
			},
			month:         "2026-10",
			wantToBudget:  850,
			wantAvailable: map[string]int64{"groceries": 0},
		},
		{
			name:       "inflow categorized into an income-group category",
			accounts:   []invariantAccount{checking},
			categories: []string{"groceries", "salary"},
			income:     map[string]bool{"salary": true},
			entries:    []BudgetEntry{groceriesSep},
			transactions: []Transaction{
				{AccountID: "checking", CategoryID: "salary", Date: "2026-09-01", Amount: 1000},
				{AccountID: "checking", CategoryID: "groceries", Date: "2026-09-10", Amount: -60},
			},
			month:         "2026-09",
			wantToBudget:  900,
			wantAvailable: map[string]int64{"groceries": 40, "salary": 1000},
		},
		{
			name:       "categorized transaction in an off-budget account",
			accounts:   []invariantAccount{checking, investment},
			categories: []string{"groceries"},
			entries:    []BudgetEntry{groceriesSep},
			transactions: []Transaction{
				paycheck,
				{AccountID: "checking", CategoryID: "groceries", Date: "2026-09-10", Amount: -60},
				{AccountID: "investment", Date: "2026-09-01", Amount: 5000},
				{AccountID: "investment", CategoryID: "groceries", Date: "2026-09-12", Amount: -500},
			},
			month:         "2026-09",
			wantToBudget:  900,
			wantAvailable: map[string]int64{"groceries": 40},
		},
		{
			name:       "transaction dated after the month",
			accounts:   []invariantAccount{checking},
			categories: []string{"groceries"},
			entries:    []BudgetEntry{groceriesSep},
			transactions: []Transaction{
				paycheck,
				{AccountID: "checking", CategoryID: "groceries", Date: "2026-09-10", Amount: -60},
				{AccountID: "checking", Date: "2026-10-01", Amount: 200},
				{AccountID: "checking", CategoryID: "groceries", Date: "2026-10-02", Amount: -30},
			},
			month:         "2026-09",
			wantToBudget:  900,
			wantAvailable: map[string]int64{"groceries": 40},
		},
		{
			name:       "budget entry in a month after the one viewed",
			accounts:   []invariantAccount{checking},
			categories: []string{"groceries"},
			entries: []BudgetEntry{
				groceriesSep,
				{CategoryID: "groceries", Month: "2026-10", Budgeted: 300},
			},
			transactions: []Transaction{
				paycheck,
				{AccountID: "checking", CategoryID: "groceries", Date: "2026-09-10", Amount: -60},
			},
			month:         "2026-09",
			wantToBudget:  900,
			wantAvailable: map[string]int64{"groceries": 40},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			toBudget, balanceSum, nonIncomeSum, available := evaluateMonth(
				tc.accounts, tc.categories, tc.income, tc.entries, tc.transactions, tc.month)

			if toBudget != tc.wantToBudget {
				t.Errorf("expected to_budget %d, got %d", tc.wantToBudget, toBudget)
			}
			for c, want := range tc.wantAvailable {
				if available[c] != want {
					t.Errorf("expected %s available %d, got %d", c, want, available[c])
				}
			}
			if toBudget+nonIncomeSum != balanceSum {
				t.Errorf("invariant broken: to_budget %d + non-income available %d != on-budget balance %d",
					toBudget, nonIncomeSum, balanceSum)
			}
		})
	}
}
