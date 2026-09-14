package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"argos/internal/budget"
	"argos/internal/db"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

var currencyPattern = regexp.MustCompile(`^[A-Z]{3}$`)

// accountTypes are the values §5.2 lists for accounts.type.
var accountTypes = map[string]bool{
	"checking":   true,
	"savings":    true,
	"credit":     true,
	"cash":       true,
	"investment": true,
	"other":      true,
}

// AccountsHandler implements the accounts endpoints (§7), backed by internal/db.
type AccountsHandler struct {
	DB *sql.DB
}

// RegisterAccountRoutes registers GET/POST /api/accounts, GET /api/accounts/{id},
// and PATCH /api/accounts/{id}.
func RegisterAccountRoutes(mux *http.ServeMux, conn *sql.DB) {
	h := &AccountsHandler{DB: conn}
	mux.HandleFunc("GET /api/accounts", h.List)
	mux.HandleFunc("POST /api/accounts", h.Create)
	mux.HandleFunc("GET /api/accounts/{id}", h.Get)
	mux.HandleFunc("PATCH /api/accounts/{id}", h.Update)
}

// account is the public JSON shape: the accounts table's own columns (§5.2)
// plus id, plus a "balance" that is computed fresh on every response — it
// is never a stored column (§5.2/§5.3) and, per this prompt, never cached.
type account struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Type     string  `json:"type"`
	OnBudget bool    `json:"on_budget"`
	Closed   bool    `json:"closed"`
	Currency string  `json:"currency"`
	Notes    *string `json:"notes"`
	Balance  int64   `json:"balance"`
}

func toAccount(a db.Account, balance int64) account {
	out := account{
		ID:       a.ID,
		Name:     a.Name,
		Type:     a.Type,
		OnBudget: a.OnBudget,
		Closed:   a.Closed,
		Currency: a.Currency,
		Balance:  balance,
	}
	if a.Notes.Valid {
		out.Notes = &a.Notes.String
	}
	return out
}

// accountBalance loads accountID's transactions and hands them to
// internal/budget's pure AccountBalance (§5.3) — the only place the sum
// is actually computed.
func accountBalance(ctx context.Context, conn *sql.DB, accountID string) (int64, error) {
	rows, err := db.ListTransactionAmountsForAccount(ctx, conn, accountID)
	if err != nil {
		return 0, err
	}
	return budget.AccountBalance(accountID, transactionAmounts(rows)), nil
}

func transactionAmounts(rows []db.TransactionAmount) []budget.Transaction {
	transactions := make([]budget.Transaction, 0, len(rows))
	for _, r := range rows {
		t := budget.Transaction{AccountID: r.AccountID, Date: r.Date, Amount: r.Amount}
		if r.DeletedAt.Valid {
			deletedAt := r.DeletedAt.Int64
			t.DeletedAt = &deletedAt
		}
		transactions = append(transactions, t)
	}
	return transactions
}

type apiErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type apiError struct {
	Error apiErrorBody `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// writeError follows §7.1: a stable machine-readable code plus a plain
// English message, nested under "error"; the server never returns
// pre-translated text (§4).
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, apiError{Error: apiErrorBody{Code: code, Message: message}})
}

func (h *AccountsHandler) List(w http.ResponseWriter, r *http.Request) {
	accounts, err := db.ListAccounts(r.Context(), h.DB)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}

	out := make([]account, 0, len(accounts))
	for _, a := range accounts {
		balance, err := accountBalance(r.Context(), h.DB, a.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
			return
		}
		out = append(out, toAccount(a, balance))
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *AccountsHandler) Get(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	a, err := db.GetAccount(r.Context(), h.DB, id)
	switch {
	case errors.Is(err, db.ErrNotFound):
		writeError(w, http.StatusNotFound, "ACCOUNT_NOT_FOUND", "no account with this id")
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}

	balance, err := accountBalance(r.Context(), h.DB, a.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, toAccount(a, balance))
}

type createAccountRequest struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Type        string  `json:"type"`
	OnBudget    *bool   `json:"on_budget"`
	Closed      bool    `json:"closed"`
	Currency    string  `json:"currency"`
	Notes       *string `json:"notes"`
	HLCPhysical *int64  `json:"hlc_physical"`
	HLCCounter  *int64  `json:"hlc_counter"`
	HLCNodeID   string  `json:"hlc_node_id"`
}

func (h *AccountsHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req createAccountRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "request body is not valid JSON")
		return
	}

	if !uuidPattern.MatchString(req.ID) {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "id must be a uuid")
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "name is required")
		return
	}
	if !accountTypes[req.Type] {
		writeError(w, http.StatusBadRequest, "INVALID_ACCOUNT_TYPE", "type must be one of checking, savings, credit, cash, investment, other")
		return
	}
	if req.OnBudget == nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "on_budget is required")
		return
	}
	if !currencyPattern.MatchString(req.Currency) {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "currency must be a 3-letter ISO 4217 code")
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

	in := db.NewAccount{
		ID:          req.ID,
		Name:        req.Name,
		Type:        req.Type,
		OnBudget:    *req.OnBudget,
		Closed:      req.Closed,
		Currency:    req.Currency,
		HLCPhysical: *req.HLCPhysical,
		HLCCounter:  *req.HLCCounter,
		HLCNodeID:   req.HLCNodeID,
	}
	if req.Notes != nil {
		in.Notes = sql.NullString{String: *req.Notes, Valid: true}
	}

	created, err := db.CreateAccount(r.Context(), h.DB, in)
	switch {
	case errors.Is(err, db.ErrAlreadyExists):
		writeError(w, http.StatusConflict, "ACCOUNT_EXISTS", "an account with this id already exists")
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}

	balance, err := accountBalance(r.Context(), h.DB, created.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, toAccount(created, balance))
}

// accountPatch holds a partial update decoded field-by-field so that a key
// present with a null value (clear notes) can be told apart from a key
// that's simply absent (leave notes alone).
type accountPatch struct {
	name        *string
	accountType *string
	onBudget    *bool
	closed      *bool
	currency    *string
	notesSet    bool
	notes       *string
	hlcPhysical *int64
	hlcCounter  *int64
	hlcNodeID   string
}

func (h *AccountsHandler) Update(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	var raw map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "request body is not valid JSON")
		return
	}

	var patch accountPatch
	for key, value := range raw {
		var errMsg string
		switch key {
		case "name":
			var s string
			if json.Unmarshal(value, &s) != nil {
				errMsg = "name must be a string"
			}
			patch.name = &s
		case "type":
			var s string
			if json.Unmarshal(value, &s) != nil {
				errMsg = "type must be a string"
			} else if !accountTypes[s] {
				writeError(w, http.StatusBadRequest, "INVALID_ACCOUNT_TYPE", "type must be one of checking, savings, credit, cash, investment, other")
				return
			}
			patch.accountType = &s
		case "on_budget":
			var b bool
			if json.Unmarshal(value, &b) != nil {
				errMsg = "on_budget must be a boolean"
			}
			patch.onBudget = &b
		case "closed":
			var b bool
			if json.Unmarshal(value, &b) != nil {
				errMsg = "closed must be a boolean"
			}
			patch.closed = &b
		case "currency":
			var s string
			if json.Unmarshal(value, &s) != nil {
				errMsg = "currency must be a string"
			} else if !currencyPattern.MatchString(s) {
				errMsg = "currency must be a 3-letter ISO 4217 code"
			}
			patch.currency = &s
		case "notes":
			patch.notesSet = true
			if string(value) != "null" {
				var s string
				if json.Unmarshal(value, &s) != nil {
					errMsg = "notes must be a string or null"
				}
				patch.notes = &s
			}
		case "hlc_physical":
			var n int64
			if json.Unmarshal(value, &n) != nil {
				errMsg = "hlc_physical must be an integer"
			}
			patch.hlcPhysical = &n
		case "hlc_counter":
			var n int64
			if json.Unmarshal(value, &n) != nil {
				errMsg = "hlc_counter must be an integer"
			}
			patch.hlcCounter = &n
		case "hlc_node_id":
			var s string
			if json.Unmarshal(value, &s) != nil {
				errMsg = "hlc_node_id must be a string"
			}
			patch.hlcNodeID = s
		}
		if errMsg != "" {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", errMsg)
			return
		}
	}

	if patch.hlcPhysical == nil || patch.hlcCounter == nil || !uuidPattern.MatchString(patch.hlcNodeID) {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "hlc_physical, hlc_counter, and hlc_node_id are required")
		return
	}

	update := db.AccountUpdate{
		Name:        patch.name,
		Type:        patch.accountType,
		OnBudget:    patch.onBudget,
		Closed:      patch.closed,
		Currency:    patch.currency,
		HLCPhysical: *patch.hlcPhysical,
		HLCCounter:  *patch.hlcCounter,
		HLCNodeID:   patch.hlcNodeID,
	}
	if patch.notesSet {
		if patch.notes != nil {
			update.Notes = &sql.NullString{String: *patch.notes, Valid: true}
		} else {
			update.Notes = &sql.NullString{}
		}
	}

	updated, err := db.UpdateAccount(r.Context(), h.DB, id, update)
	switch {
	case errors.Is(err, db.ErrNotFound):
		writeError(w, http.StatusNotFound, "ACCOUNT_NOT_FOUND", "no account with this id")
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}

	balance, err := accountBalance(r.Context(), h.DB, updated.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, toAccount(updated, balance))
}
