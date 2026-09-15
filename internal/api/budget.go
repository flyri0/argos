package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"

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

// onBudgetAccountIDs returns the ids of every non-deleted on-budget account.
// A transaction whose account is missing from the map (off-budget or
// deleted) never counts toward activity (§5.3).
func onBudgetAccountIDs(ctx context.Context, conn *sql.DB) (map[string]bool, []string, error) {
	accounts, err := db.ListAccounts(ctx, conn)
	if err != nil {
		return nil, nil, err
	}
	ids := make(map[string]bool, len(accounts))
	ordered := make([]string, 0, len(accounts))
	for _, a := range accounts {
		if a.OnBudget {
			ids[a.ID] = true
			ordered = append(ordered, a.ID)
		}
	}
	return ids, ordered, nil
}

// rollupCategory loads categoryID's history and hands it to
// budget.RollupCategory.
func rollupCategory(ctx context.Context, conn *sql.DB, onBudget map[string]bool, categoryID, month string) (categoryBudget, error) {
	entryRows, err := db.ListBudgetEntriesForCategory(ctx, conn, categoryID)
	if err != nil {
		return categoryBudget{}, err
	}
	txnRows, err := db.ListTransactionsForCategory(ctx, conn, categoryID)
	if err != nil {
		return categoryBudget{}, err
	}

	entries := make([]budget.BudgetEntry, 0, len(entryRows))
	for _, e := range entryRows {
		entries = append(entries, budget.BudgetEntry{CategoryID: e.CategoryID, Month: e.Month, Budgeted: e.Budgeted})
	}
	transactions := make([]budget.Transaction, 0, len(txnRows))
	for _, t := range txnRows {
		bt := budget.Transaction{
			AccountID: t.AccountID, CategoryID: t.CategoryID.String, Date: t.Date, Amount: t.Amount,
			OnBudget: onBudget[t.AccountID],
		}
		if t.DeletedAt.Valid {
			deletedAt := t.DeletedAt.Int64
			bt.DeletedAt = &deletedAt
		}
		transactions = append(transactions, bt)
	}

	figures := budget.RollupCategory(entries, transactions, categoryID, month)
	return categoryBudget{
		CategoryID: categoryID,
		Month:      month,
		Budgeted:   figures.Budgeted,
		Activity:   figures.Activity,
		Available:  figures.Available,
	}, nil
}

func (h *BudgetHandler) Get(w http.ResponseWriter, r *http.Request) {
	month := r.PathValue("month")
	if !monthPattern.MatchString(month) {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "month must be in YYYY-MM format")
		return
	}
	ctx := r.Context()

	onBudget, onBudgetIDs, err := onBudgetAccountIDs(ctx, h.DB)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	groups, err := db.ListCategoryGroups(ctx, h.DB)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	incomeGroups := make(map[string]bool)
	for _, g := range groups {
		if g.IsIncome {
			incomeGroups[g.ID] = true
		}
	}
	categories, err := db.ListCategories(ctx, h.DB)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}

	out := make([]categoryBudget, 0, len(categories))
	nonIncomeAvailable := make([]int64, 0, len(categories))
	for _, c := range categories {
		figures, err := rollupCategory(ctx, h.DB, onBudget, c.ID, month)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
			return
		}
		out = append(out, figures)
		if !incomeGroups[c.GroupID] {
			nonIncomeAvailable = append(nonIncomeAvailable, figures.Available)
		}
	}

	balances := make([]int64, 0, len(onBudgetIDs))
	for _, id := range onBudgetIDs {
		rows, err := db.ListTransactionAmountsForAccount(ctx, h.DB, id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
			return
		}
		balances = append(balances, budget.BalanceThrough(id, month, transactionAmounts(rows)))
	}

	writeJSON(w, http.StatusOK, monthBudget{
		Month:      month,
		ToBudget:   budget.ToBudget(balances, nonIncomeAvailable),
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
	case errors.Is(err, db.ErrStaleWrite):
		writeError(w, http.StatusConflict, "STALE_WRITE", "a newer write to this budget entry already exists")
		return
	case errors.Is(err, db.ErrCategoryNotFound):
		writeError(w, http.StatusNotFound, "CATEGORY_NOT_FOUND", "no category with this id")
		return
	case errors.Is(err, db.ErrIncomeCategoryNotBudgetable):
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "income-group categories cannot be budgeted")
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}

	onBudget, _, err := onBudgetAccountIDs(r.Context(), h.DB)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	figures, err := rollupCategory(r.Context(), h.DB, onBudget, categoryID, month)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, figures)
}
