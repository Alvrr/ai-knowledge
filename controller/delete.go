package controller

import (
	"fmt"
	"net/http"
	"strconv"

	"go-pgvector-gemini/db"
)

func (c *DocumentController) HandleDeleteDocument(w http.ResponseWriter, r *http.Request) {
	_, collection := c.ResolveEmbedderAndCollection(r, "")

	idStr := r.PathValue("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		WriteError(w, http.StatusBadRequest, "ID dokumen harus berupa angka")
		return
	}

	if err := db.DeleteDocumentFrom(r.Context(), c.pool, collection, id); err != nil {
		WriteError(w, http.StatusNotFound, err.Error())
		return
	}

	WriteJSON(w, http.StatusOK, map[string]any{
		"status":     "success",
		"collection": collection,
		"message":    fmt.Sprintf("Dokumen dengan ID %d berhasil dihapus dari '%s'", id, collection),
	})
}
