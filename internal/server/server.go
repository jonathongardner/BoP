package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jonathongardner/go-starter/internal/store"
)

type Server struct {
	store *store.Store
	index []byte
}

func New(st *store.Store, indexHTML []byte) *Server {
	return &Server{store: st, index: indexHTML}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.HandleFunc("GET /api/persons", s.handleListPersons)
	mux.HandleFunc("POST /api/persons", s.handleCreatePerson)
	mux.HandleFunc("GET /api/accounts", s.handleListAccounts)
	mux.HandleFunc("POST /api/accounts", s.handleCreateAccount)
	mux.HandleFunc("DELETE /api/accounts/{id}/transactions/{txid}", s.handleDeleteTransaction)
	mux.HandleFunc("DELETE /api/accounts/{id}", s.handleDeleteAccount)
	mux.HandleFunc("GET /api/accounts/{id}/transactions", s.handleListTransactions)
	mux.HandleFunc("POST /api/accounts/{id}/deposit", s.handleDeposit)
	mux.HandleFunc("POST /api/accounts/{id}/withdraw", s.handleWithdraw)

	return withMiddleware(mux)
}

func withMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(s.index)
}

func (s *Server) handleListPersons(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListPersonsWithAccounts(r.Context())
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if list == nil {
		list = []store.PersonWithAccounts{}
	}
	writeJSON(w, http.StatusOK, list)
}

type createPersonBody struct {
	Name string `json:"name"`
}

func (s *Server) handleCreatePerson(w http.ResponseWriter, r *http.Request) {
	var body createPersonBody
	if err := decodeJSON(w, r, &body); err != nil {
		return
	}
	p, err := s.store.CreatePerson(r.Context(), body.Name)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			httpError(w, http.StatusConflict, "a person with that name already exists")
			return
		}
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, p)
}

func (s *Server) handleListAccounts(w http.ResponseWriter, r *http.Request) {
	accts, err := s.store.ListAccounts(r.Context())
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if accts == nil {
		accts = []store.Account{}
	}
	writeJSON(w, http.StatusOK, accts)
}

type createAccountBody struct {
	Name     string `json:"name"`
	PersonID int64  `json:"person_id"`
}

func (s *Server) handleCreateAccount(w http.ResponseWriter, r *http.Request) {
	var body createAccountBody
	if err := decodeJSON(w, r, &body); err != nil {
		return
	}
	acct, err := s.store.CreateAccount(r.Context(), body.PersonID, body.Name)
	if err != nil {
		if errors.Is(err, store.ErrPersonNotFound) {
			httpError(w, http.StatusBadRequest, "person not found")
			return
		}
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			httpError(w, http.StatusConflict, "this person already has an account with that name")
			return
		}
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, acct)
}

func (s *Server) handleListTransactions(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r.PathValue("id"))
	if !ok {
		return
	}
	txs, err := s.store.ListTransactions(r.Context(), id)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if txs == nil {
		txs = []store.Transaction{}
	}
	writeJSON(w, http.StatusOK, txs)
}

type amountBody struct {
	AmountCents int64  `json:"amount_cents"`
	Description string `json:"description"`
}

func (s *Server) handleDeposit(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r.PathValue("id"))
	if !ok {
		return
	}
	var body amountBody
	if err := decodeJSON(w, r, &body); err != nil {
		return
	}
	if err := s.store.Deposit(r.Context(), id, body.AmountCents, body.Description); err != nil {
		writeStoreErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleWithdraw(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r.PathValue("id"))
	if !ok {
		return
	}
	var body amountBody
	if err := decodeJSON(w, r, &body); err != nil {
		return
	}
	if err := s.store.Withdraw(r.Context(), id, body.AmountCents, body.Description); err != nil {
		writeStoreErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteAccount(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r.PathValue("id"))
	if !ok {
		return
	}
	if err := s.store.DeleteAccount(r.Context(), id); err != nil {
		writeStoreErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteTransaction(w http.ResponseWriter, r *http.Request) {
	aid, ok := parseID(w, r.PathValue("id"))
	if !ok {
		return
	}
	tid, ok := parseTxID(w, r.PathValue("txid"))
	if !ok {
		return
	}
	if err := s.store.DeleteTransaction(r.Context(), aid, tid); err != nil {
		writeStoreErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func parseID(w http.ResponseWriter, raw string) (int64, bool) {
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		httpError(w, http.StatusBadRequest, "invalid account id")
		return 0, false
	}
	return id, true
}

func parseTxID(w http.ResponseWriter, raw string) (int64, bool) {
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		httpError(w, http.StatusBadRequest, "invalid transaction id")
		return 0, false
	}
	return id, true
}

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	defer r.Body.Close()
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		httpError(w, http.StatusBadRequest, "invalid json")
		return err
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		httpError(w, http.StatusBadRequest, "invalid json")
		return err
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func httpError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func writeStoreErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrInsufficientFunds):
		httpError(w, http.StatusBadRequest, "insufficient funds")
	case errors.Is(err, store.ErrInvalidAmount):
		httpError(w, http.StatusBadRequest, "amount must be positive")
	case errors.Is(err, store.ErrAccountNotFound):
		httpError(w, http.StatusNotFound, "account not found")
	case errors.Is(err, store.ErrPersonNotFound):
		httpError(w, http.StatusBadRequest, "person not found")
	case errors.Is(err, store.ErrDescriptionTooLong):
		httpError(w, http.StatusBadRequest, "description must be at most 255 characters")
	case errors.Is(err, store.ErrAccountHasBalance):
		httpError(w, http.StatusBadRequest, "account balance must be zero to delete")
	case errors.Is(err, store.ErrTransactionNotFound):
		httpError(w, http.StatusNotFound, "transaction not found")
	case errors.Is(err, store.ErrDeleteTransactionBalance):
		httpError(w, http.StatusBadRequest, "cannot delete transaction: balance would become negative")
	default:
		httpError(w, http.StatusInternalServerError, err.Error())
	}
}
