package control

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"scout.local/scout/internal/store"
)

type APIError struct {
	Error APIErrorBody `json:"error"`
}
type APIErrorBody struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"requestId"`
	Retryable bool   `json:"retryable"`
}

func writeError(w http.ResponseWriter, r *http.Request, status int, code, message string, retryable bool) {
	requestID := r.Header.Get("X-Request-ID")
	if requestID == "" {
		requestID = store.NewID()
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Request-ID", requestID)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(APIError{Error: APIErrorBody{Code: code, Message: message, RequestID: requestID, Retryable: retryable}})
}

func writeMappedError(w http.ResponseWriter, r *http.Request, err error) {
	if err == nil {
		return
	}
	status, code, message, retryable := mapError(err)
	writeError(w, r, status, code, message, retryable)
}

func mapError(err error) (int, string, string, bool) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return http.StatusNotFound, "not_found", "Resource not found", false
	case errors.Is(err, store.ErrUnauthorized):
		return http.StatusUnauthorized, "unauthorized", "Authentication required", false
	case errors.Is(err, store.ErrForbidden):
		return http.StatusForbidden, "forbidden", "Action not permitted", false
	case errors.Is(err, store.ErrExpired):
		return http.StatusUnauthorized, "expired", "The presented authorization has expired", false
	case errors.Is(err, store.ErrRevoked):
		return http.StatusForbidden, "revoked", "The presented authorization has been revoked", false
	case errors.Is(err, store.ErrRecentMFA):
		return http.StatusForbidden, "recent_mfa_required", "Recent MFA verification is required for this action", false
	case errors.Is(err, store.ErrConflict), errors.Is(err, store.ErrDuplicate):
		return http.StatusConflict, "conflict", "State conflict; refresh and retry", false
	case errors.Is(err, store.ErrBackpressure):
		return http.StatusServiceUnavailable, "backpressure", "Service is temporarily paused or at capacity", true
	case errors.Is(err, store.ErrIncidentCap):
		return http.StatusServiceUnavailable, "incident_capacity", "Incident capacity has been reached", true
	case errors.Is(err, store.ErrInvalid):
		return http.StatusBadRequest, "invalid_request", "Request failed validation", false
	default:
		return http.StatusInternalServerError, "internal_error", "Request could not be completed", false
	}
}

func backendError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("backend operation failed: %w", err)
}
