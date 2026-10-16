package controller

import (
	"encoding/json"
	"log"
	"net/http"
)

// WriteJSON sends a JSON response with the given status code and payload.
func WriteJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.Printf("Gagal encode JSON response: %v", err)
	}
}

// WriteError sends a JSON error response with the given status code and message.
func WriteError(w http.ResponseWriter, status int, message string) {
	WriteJSON(w, status, map[string]any{
		"status":  "error",
		"message": message,
	})
}
