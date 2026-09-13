package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"sort"

	"argos/internal/budget"
	"argos/internal/db"
)

var monthPattern = regexp.MustCompile(`^\d{4}-\d{2}$`)

// BudgetHandler implements the budget endpoints (§7.3), backed by
// internal/db and internal/budget's pure rollover functions (§5.3).
type BudgetHandler struct {
	DB *sql.DB
}

// RegisterBudgetRoutes registers GET /api/budget/{month} and
// PUT /api/budget/{month}/{category_id}.
func RegisterBudgetRoutes(mux *http.ServeMux, conn *sql.DB) {
	h := &BudgetHandler{DB: conn}
	mux.HandleFunc("GET /api/budget/{month}", h.Get)
	mux.HandleFunc("PUT /api/budget/{month}/{category_id}", h.Set)
}

// categoryBudget is the public JSON shape for one category's figures in a
// given month (§5.3): budgeted is the stored amount; activity and
// available are always computed, never stored.
type categoryBudget struct {
	CategoryID string `json:"category_id"`
	Month      string `json:"month"`
	Budgeted   int64  `json:"budgeted"`
	Activity   int64  `json:"activity"`
	Available  int64  `json:"available"`
}

// monthBudget is GET /api/budget/{month}'s response envelope (§7.1: a
// single logical resource, so an object rather than a bare array):
// per-category figures alongside to_budget (§5.3), the one budgeting
// figure that isn't scoped to a single category.
type monthBudget struct {
	Month      string           `json:"month"`
	ToBudget   int64            `json:"to_budget"`
	Categories []categoryBudget `json:"categories"`
}

// rollupCategory walks categoryID's full budget_entries/transaction history
// up to and including month, applying internal/budget's Available() rule
// (§5.3) forward from the earliest relevant month, so a rollover or an
// overspend from arbitrarily far back still reaches the target month
// correctly.
func rollupCategory(entries []db.BudgetEntry, transactions []db.Transaction, categoryID, month string) categoryBudget {
	budgetedByMonth := make(map[string]int64, len(entries))
	for _, e := range entries {
		budgetedByMonth[e.Month] = e.Budgeted
	}

	budgetTxns := make([]budget.Transaction, 0, len(transactions))
	months := map[string]bool{month: true}
	for _, t := range transactions {
		budgetTxns = append(budgetTxns, budget.Transaction{CategoryID: t.CategoryID.String, Date: t.Date, Amount: t.Amount})
		if len(t.Date) >= 7 {
			months[t.Date[:7]] = true
		}
	}
	for m := range budgetedByMonth {
		months[m] = true
	}

	ordered := make([]string, 0, len(months))
	for m := range months {
		if m <= month {
			ordered = append(ordered, m)
		}
	}
	sort.Strings(ordered)

	var previousAvailable int64
	out := categoryBudget{CategoryID: categoryID, Month: month}
	for _, m := range ordered {
		budgeted := budgetedByMonth[m]
		activity := budget.Activity(budgetTxns, categoryID, m)
		available := budget.Available(previousAvailable, budgeted, activity)
		previousAvailable = available
		if m == month {
			out.Budgeted = budgeted
			out.Activity = activity
			out.Available = available
		}
	}
	return out
}

func (h *BudgetHandler) Get(w http.ResponseWriter, r *http.Request) {
	month := r.PathValue("month")
	if !monthPattern.MatchString(month) {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "month must be in YYYY-MM format")
		return
	}

	categories, err := db.ListCategories(r.Context(), h.DB)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}

	out := make([]categoryBudget, 0, len(categories))
	// Accumulated alongside the per-category rollup rather than in a second
	// pass over the same entries: §5.3's "everything already budgeted
	// across all months to date" means every non-deleted budget_entries row
	// up to and including the requested month, across every category.
	var budgetedToDate int64
	for _, c := range categories {
		entries, err := db.ListBudgetEntriesForCategory(r.Context(), h.DB, c.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
			return
		}
		transactions, err := db.ListTransactionsForCategory(r.Context(), h.DB, c.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
			return
		}
		out = append(out, rollupCategory(entries, transactions, c.ID, month))

		for _, e := range entries {
			if e.Month <= month {
				budgetedToDate += e.Budgeted
			}
		}
	}

	accounts, err := db.ListAccounts(r.Context(), h.DB)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	// §5.3: to_budget only counts on-budget accounts — off-budget balances
	// (e.g. a tracked investment account) never affect what there is to budget.
	balances := make([]int64, 0, len(accounts))
	for _, a := range accounts {
		if !a.OnBudget {
			continue
		}
		balance, err := accountBalance(r.Context(), h.DB, a.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
			return
		}
		balances = append(balances, balance)
	}

	writeJSON(w, http.StatusOK, monthBudget{
		Month:      month,
		ToBudget:   budget.ToBudget(balances, budgetedToDate),
		Categories: out,
	})
}

type setBudgetRequest struct {
	ID          string `json:"id"`
	Budgeted    *int64 `json:"budgeted"`
	HLCPhysical *int64 `json:"hlc_physical"`
	HLCCounter  *int64 `json:"hlc_counter"`
	HLCNodeID   string `json:"hlc_node_id"`
}

func (h *BudgetHandler) Set(w http.ResponseWriter, r *http.Request) {
	month := r.PathValue("month")
	categoryID := r.PathValue("category_id")

	if !monthPattern.MatchString(month) {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "month must be in YYYY-MM format")
		return
	}
	if !uuidPattern.MatchString(categoryID) {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "category_id must be a uuid")
		return
	}

	var req setBudgetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "request body is not valid JSON")
		return
	}
	if req.Budgeted == nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "budgeted is required")
		return
	}
	if !uuidPattern.MatchString(req.ID) {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "id must be a uuid")
		return
	}
	if req.HLCPhysical == nil || req.HLCCounter == nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "hlc_physical and hlc_counter are required")
		return
	}
	if !uuidPattern.MatchString(req.HLCNodeID) {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "hlc_node_id must be a uuid")
		return
	}

	err := db.SetBudgetedAmount(r.Context(), h.DB, req.ID, categoryID, month, *req.Budgeted, *req.HLCPhysical, *req.HLCCounter, req.HLCNodeID)
	switch {
	case errors.Is(err, db.ErrCategoryNotFound):
		writeError(w, http.StatusNotFound, "CATEGORY_NOT_FOUND", "no category with this id")
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}

	entries, err := db.ListBudgetEntriesForCategory(r.Context(), h.DB, categoryID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	transactions, err := db.ListTransactionsForCategory(r.Context(), h.DB, categoryID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rollupCategory(entries, transactions, categoryID, month))
}
