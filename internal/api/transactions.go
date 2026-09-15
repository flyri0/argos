package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"

	"argos/internal/db"
)

var datePattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// TransactionsHandler implements the transactions endpoints (§7.3), backed by internal/db.
type TransactionsHandler struct {
	DB *sql.DB
}

// RegisterTransactionRoutes registers GET/POST /api/transactions and
// PATCH/DELETE /api/transactions/{id}.
func RegisterTransactionRoutes(mux *http.ServeMux, conn *sql.DB) {
	h := &TransactionsHandler{DB: conn}
	mux.HandleFunc("GET /api/transactions", h.List)
	mux.HandleFunc("POST /api/transactions", h.Create)
	mux.HandleFunc("PATCH /api/transactions/{id}", h.Update)
	mux.HandleFunc("DELETE /api/transactions/{id}", h.Delete)
}

// transaction is the public JSON shape for a transactions row: its own
// columns (§5.2) plus id, minus sync metadata. parent_id is always null in
// the MVP (splits aren't implemented) but is still exposed for forward
// compatibility with the column already existing in the schema.
type transaction struct {
	ID         string  `json:"id"`
	AccountID  string  `json:"account_id"`
	CategoryID *string `json:"category_id"`
	PayeeID    *string `json:"payee_id"`
	ParentID   *string `json:"parent_id"`
	Date       string  `json:"date"`
	Amount     int64   `json:"amount"`
	Cleared    bool    `json:"cleared"`
	Notes      string  `json:"notes"`
	TransferID *string `json:"transfer_id"`
}

func toTransaction(t db.Transaction) transaction {
	out := transaction{
		ID:        t.ID,
		AccountID: t.AccountID,
		Date:      t.Date,
		Amount:    t.Amount,
		Cleared:   t.Cleared,
		Notes:     t.Notes,
	}
	if t.CategoryID.Valid {
		out.CategoryID = &t.CategoryID.String
	}
	if t.PayeeID.Valid {
		out.PayeeID = &t.PayeeID.String
	}
	if t.ParentID.Valid {
		out.ParentID = &t.ParentID.String
	}
	if t.TransferID.Valid {
		out.TransferID = &t.TransferID.String
	}
	return out
}

func writeTransactionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, db.ErrNotFound):
		writeError(w, http.StatusNotFound, "TRANSACTION_NOT_FOUND", "no transaction with this id")
	case errors.Is(err, db.ErrAccountNotFound):
		writeError(w, http.StatusNotFound, "ACCOUNT_NOT_FOUND", "no account with the given id")
	case errors.Is(err, db.ErrAccountClosed):
		writeError(w, http.StatusConflict, "ACCOUNT_CLOSED", "account is closed and cannot accept new transactions")
	case errors.Is(err, db.ErrCategoryNotFound):
		writeError(w, http.StatusNotFound, "CATEGORY_NOT_FOUND", "no category with the given id")
	case errors.Is(err, db.ErrPayeeNotFound):
		writeError(w, http.StatusNotFound, "PAYEE_NOT_FOUND", "no payee with the given id")
	case errors.Is(err, db.ErrAlreadyExists):
		writeError(w, http.StatusConflict, "TRANSACTION_EXISTS", "a transaction with this id already exists")
	case errors.Is(err, db.ErrStaleWrite):
		writeError(w, http.StatusConflict, "STALE_WRITE", "a newer write to this transaction (or its transfer sibling) already exists")
	case errors.Is(err, db.ErrTransferLegFieldImmutable):
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "account_id and category_id cannot be changed on a transfer leg")
	default:
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
	}
}

func (h *TransactionsHandler) List(w http.ResponseWriter, r *http.Request) {
	accountID := r.URL.Query().Get("account_id")
	if accountID == "" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "account_id is required")
		return
	}

	transactions, err := db.ListTransactionsForAccount(r.Context(), h.DB, accountID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}

	out := make([]transaction, 0, len(transactions))
	for _, t := range transactions {
		out = append(out, toTransaction(t))
	}
	writeJSON(w, http.StatusOK, out)
}

type createTransactionRequest struct {
	ID          string  `json:"id"`
	AccountID   string  `json:"account_id"`
	CategoryID  *string `json:"category_id"`
	PayeeID     *string `json:"payee_id"`
	Date        string  `json:"date"`
	Amount      *int64  `json:"amount"`
	Cleared     bool    `json:"cleared"`
	Notes       string  `json:"notes"`
	HLCPhysical *int64  `json:"hlc_physical"`
	HLCCounter  *int64  `json:"hlc_counter"`
	HLCNodeID   string  `json:"hlc_node_id"`

	TransferAccountID     *string `json:"transfer_account_id"`
	TransferTransactionID *string `json:"transfer_transaction_id"`
	TransferHLCPhysical   *int64  `json:"transfer_hlc_physical"`
	TransferHLCCounter    *int64  `json:"transfer_hlc_counter"`
	TransferHLCNodeID     *string `json:"transfer_hlc_node_id"`
}

func (h *TransactionsHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req createTransactionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "request body is not valid JSON")
		return
	}

	if !uuidPattern.MatchString(req.ID) {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "id must be a uuid")
		return
	}
	if !uuidPattern.MatchString(req.AccountID) {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "account_id must be a uuid")
		return
	}
	if !datePattern.MatchString(req.Date) {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "date must be in YYYY-MM-DD format")
		return
	}
	if req.Amount == nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "amount is required")
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
	if req.PayeeID != nil && !uuidPattern.MatchString(*req.PayeeID) {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "payee_id must be a uuid")
		return
	}

	var payeeID sql.NullString
	if req.PayeeID != nil {
		payeeID = sql.NullString{String: *req.PayeeID, Valid: true}
	}

	if req.TransferAccountID != nil {
		if req.CategoryID != nil {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "category_id must not be set for a transfer")
			return
		}
		if !uuidPattern.MatchString(*req.TransferAccountID) {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "transfer_account_id must be a uuid")
			return
		}
		if *req.TransferAccountID == req.AccountID {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "transfer_account_id must be a different account")
			return
		}
		if req.TransferTransactionID == nil || !uuidPattern.MatchString(*req.TransferTransactionID) {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "transfer_transaction_id must be a uuid")
			return
		}
		if *req.TransferTransactionID == req.ID {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "transfer_transaction_id must be different from id")
			return
		}
		if req.TransferHLCPhysical == nil || req.TransferHLCCounter == nil {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "transfer_hlc_physical and transfer_hlc_counter are required")
			return
		}
		if req.TransferHLCNodeID == nil || !uuidPattern.MatchString(*req.TransferHLCNodeID) {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "transfer_hlc_node_id must be a uuid")
			return
		}

		in := db.NewTransfer{
			ID:                    req.ID,
			AccountID:             req.AccountID,
			TransferTransactionID: *req.TransferTransactionID,
			TransferAccountID:     *req.TransferAccountID,
			PayeeID:               payeeID,
			Date:                  req.Date,
			Amount:                *req.Amount,
			Cleared:               req.Cleared,
			Notes:                 req.Notes,
			HLCPhysical:           *req.HLCPhysical,
			HLCCounter:            *req.HLCCounter,
			HLCNodeID:             req.HLCNodeID,
			TransferHLCPhysical:   *req.TransferHLCPhysical,
			TransferHLCCounter:    *req.TransferHLCCounter,
			TransferHLCNodeID:     *req.TransferHLCNodeID,
		}

		t1, t2, err := db.CreateTransfer(r.Context(), h.DB, in)
		if err != nil {
			writeTransactionError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, []transaction{toTransaction(t1), toTransaction(t2)})
		return
	}

	if req.CategoryID != nil && !uuidPattern.MatchString(*req.CategoryID) {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "category_id must be a uuid")
		return
	}

	var categoryID sql.NullString
	if req.CategoryID != nil {
		categoryID = sql.NullString{String: *req.CategoryID, Valid: true}
	}

	in := db.NewTransaction{
		ID:          req.ID,
		AccountID:   req.AccountID,
		CategoryID:  categoryID,
		PayeeID:     payeeID,
		Date:        req.Date,
		Amount:      *req.Amount,
		Cleared:     req.Cleared,
		Notes:       req.Notes,
		HLCPhysical: *req.HLCPhysical,
		HLCCounter:  *req.HLCCounter,
		HLCNodeID:   req.HLCNodeID,
	}

	created, err := db.CreateTransaction(r.Context(), h.DB, in)
	if err != nil {
		writeTransactionError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, toTransaction(created))
}

func (h *TransactionsHandler) Update(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	var raw map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "request body is not valid JSON")
		return
	}

	hlcPhysical, hlcCounter, hlcNodeID, ok := parseHLC(w, raw)
	if !ok {
		return
	}

	var patch db.TransactionUpdate
	patch.HLCPhysical = hlcPhysical
	patch.HLCCounter = hlcCounter
	patch.HLCNodeID = hlcNodeID

	for key, value := range raw {
		var errMsg string
		switch key {
		case "account_id":
			var s string
			if json.Unmarshal(value, &s) != nil || !uuidPattern.MatchString(s) {
				errMsg = "account_id must be a uuid"
			}
			patch.AccountID = &s
		case "category_id":
			if string(value) == "null" {
				patch.CategoryID = &sql.NullString{}
			} else {
				var s string
				if json.Unmarshal(value, &s) != nil || !uuidPattern.MatchString(s) {
					errMsg = "category_id must be a uuid or null"
				}
				patch.CategoryID = &sql.NullString{String: s, Valid: true}
			}
		case "payee_id":
			if string(value) == "null" {
				patch.PayeeID = &sql.NullString{}
			} else {
				var s string
				if json.Unmarshal(value, &s) != nil || !uuidPattern.MatchString(s) {
					errMsg = "payee_id must be a uuid or null"
				}
				patch.PayeeID = &sql.NullString{String: s, Valid: true}
			}
		case "date":
			var s string
			if json.Unmarshal(value, &s) != nil || !datePattern.MatchString(s) {
				errMsg = "date must be in YYYY-MM-DD format"
			}
			patch.Date = &s
		case "amount":
			var n int64
			if json.Unmarshal(value, &n) != nil {
				errMsg = "amount must be an integer"
			}
			patch.Amount = &n
		case "cleared":
			var b bool
			if json.Unmarshal(value, &b) != nil {
				errMsg = "cleared must be a boolean"
			}
			patch.Cleared = &b
		case "notes":
			var s string
			if json.Unmarshal(value, &s) != nil {
				errMsg = "notes must be a string"
			}
			patch.Notes = &s
		}
		if errMsg != "" {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", errMsg)
			return
		}
	}

	updated, err := db.UpdateTransaction(r.Context(), h.DB, id, patch)
	if err != nil {
		writeTransactionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toTransaction(updated))
}

func (h *TransactionsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	var raw map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "request body is not valid JSON")
		return
	}

	hlcPhysical, hlcCounter, hlcNodeID, ok := parseHLC(w, raw)
	if !ok {
		return
	}

	_, err := db.DeleteTransaction(r.Context(), h.DB, id, hlcPhysical, hlcCounter, hlcNodeID)
	if err != nil {
		writeTransactionError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// parseHLC reads and validates the required hlc_physical/hlc_counter/
// hlc_node_id triple (§5.1) out of a decoded request body. On failure it
// writes the error response itself and returns ok=false.
func parseHLC(w http.ResponseWriter, raw map[string]json.RawMessage) (physical, counter int64, nodeID string, ok bool) {
	if v, present := raw["hlc_physical"]; present {
		if json.Unmarshal(v, &physical) != nil {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "hlc_physical must be an integer")
			return 0, 0, "", false
		}
	}
	if v, present := raw["hlc_counter"]; present {
		if json.Unmarshal(v, &counter) != nil {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "hlc_counter must be an integer")
			return 0, 0, "", false
		}
	}
	if v, present := raw["hlc_node_id"]; present {
		if json.Unmarshal(v, &nodeID) != nil {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "hlc_node_id must be a string")
			return 0, 0, "", false
		}
	}
	if _, present := raw["hlc_physical"]; !present {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "hlc_physical, hlc_counter, and hlc_node_id are required")
		return 0, 0, "", false
	}
	if _, present := raw["hlc_counter"]; !present {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "hlc_physical, hlc_counter, and hlc_node_id are required")
		return 0, 0, "", false
	}
	if !uuidPattern.MatchString(nodeID) {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "hlc_physical, hlc_counter, and hlc_node_id are required")
		return 0, 0, "", false
	}
	return physical, counter, nodeID, true
}
