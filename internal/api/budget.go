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
	}
	writeJSON(w, http.StatusOK, out)
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
