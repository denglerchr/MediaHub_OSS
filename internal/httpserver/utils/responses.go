package utils

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
)

// MaxJSONBodyBytes bounds the maximum size of JSON request payloads (1 MiB).
const MaxJSONBodyBytes int64 = 1 << 20

// ReadJSONBody reads at most MaxJSONBodyBytes from r.Body.
// When status == 0, reading succeeded and bodyBytes contains the payload.
func ReadJSONBody(w http.ResponseWriter, r *http.Request) ([]byte, int, string) {
	if r == nil || r.Body == nil {
		return nil, http.StatusBadRequest, "Invalid JSON payload"
	}
	r.Body = http.MaxBytesReader(w, r.Body, MaxJSONBodyBytes)
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			return nil, http.StatusRequestEntityTooLarge, "Request body too large"
		}
		return nil, http.StatusBadRequest, "Invalid JSON payload"
	}
	return bodyBytes, 0, ""
}

// DecodeJSONBody reads a bounded JSON request body and unmarshals it into dst.
// When status == 0, decoding succeeded.
func DecodeJSONBody(w http.ResponseWriter, r *http.Request, dst any) (int, string) {
	bodyBytes, status, msg := ReadJSONBody(w, r)
	if status != 0 {
		return status, msg
	}
	if err := json.Unmarshal(bodyBytes, dst); err != nil {
		return http.StatusBadRequest, "Invalid JSON payload"
	}
	return 0, ""
}

// ErrorResponse matches the JSON structure used by the API handlers.
// Defined locally to avoid circular dependencies with the handlers package.
type ErrorResponse struct {
	Error string `json:"error"`
}

// MessageResponse is a standard format for simple API messages.
type MessageResponse struct {
	Message string `json:"message"`
}

// respondWithError writes a JSON error response to ensure consistency with the API.
func RespondWithError(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(ErrorResponse{Error: message}); err != nil {
		log.Printf("ERROR: Failed to encode web error response: %v", err)
	}
}

// respondWithJSON writes a JSON response to ensure consistency with the API.
func RespondWithJSON(w http.ResponseWriter, code int, payload any) {
	response, err := json.Marshal(payload)
	if err != nil {
		// Fallback to manual JSON string if marshaling fails, keeping format consistent
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"Failed to marshal JSON response"}`))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	w.Write(response)
}
