package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// allocationRefRequest is the JSON body for an attestor-mode (Option A) allocation upload.
type allocationRefRequest struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
}

// allocationUploadResponse is returned after a successful allocation upload/registration.
type allocationUploadResponse struct {
	SHA256 string `json:"sha256"`
}

// allocationFileJSON is one curated allocation file's governance metadata.
type allocationFileJSON struct {
	Type               string    `json:"type"`
	SHA256             string    `json:"sha256"`
	Status             string    `json:"status"`
	ApprovedByProposal *string   `json:"approved_by_proposal,omitempty"`
	UploadedAt         time.Time `json:"uploaded_at"`
}

// allocationListResponse is the list of a launch's allocation files.
type allocationListResponse struct {
	Allocations []allocationFileJSON `json:"allocations"`
}

// POST /launch/{id}/allocations/{type}
//
// Accepts two modes based on Content-Type, identical to the genesis upload:
//
//   - application/json (attestor mode): {"url":"https://...","sha256":"<64-char hex>"}
//   - application/octet-stream (host mode, requires COORD_GENESIS_HOST_MODE=true):
//     raw allocation-file bytes (capped at GenesisMaxBytes). The content is opaque to
//     coordd — gentool emits CSV/TSV, not JSON — so it is stored and hashed, not parsed.
//
// The file lands in PENDING status; a re-upload (new hash) invalidates any prior
// approval. Approval/rejection is governed by APPROVE_ALLOCATION_FILE proposals.
//
// @Summary      Upload an allocation file
// @Description  Committee members only.
// @Description  Attestor mode (default): register external URL + SHA-256.
// @Description  Host mode: upload raw bytes (requires COORD_GENESIS_HOST_MODE=true).
// @Tags         allocations
// @Security     BearerAuth
// @Accept       application/json
// @Accept       application/octet-stream
// @Produce      json
// @Param        id    path      string  true  "Launch UUID"
// @Param        type  path      string  true  "Allocation type" Enums(accounts,claims,grants,authz,feegrant)
// @Success      200   {object}  allocationUploadResponse
// @Failure      400   {object}  errorEnvelope
// @Failure      401   {object}  errorEnvelope
// @Failure      403   {object}  errorEnvelope
// @Failure      413   {object}  errorEnvelope
// @Router       /launch/{id}/allocations/{type} [post]
func (s *Server) handleAllocationUpload(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "launch id must be a valid UUID")
		return
	}
	allocType := chi.URLParam(r, "type")
	callerAddr := operatorFromContext(r.Context())

	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "application/json") {
		s.handleAllocationUploadRef(w, r, id, allocType, callerAddr)
	} else {
		s.handleAllocationUploadBytes(w, r, id, allocType, callerAddr)
	}
}

// handleAllocationUploadRef handles attestor-mode (Option A) uploads.
func (s *Server) handleAllocationUploadRef(w http.ResponseWriter, r *http.Request, id uuid.UUID, allocType, callerAddr string) {
	var req allocationRefRequest
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBody)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "request body must be valid JSON")
		return
	}
	if req.URL == "" {
		writeError(w, http.StatusBadRequest, "missing_url", "url is required")
		return
	}
	if req.SHA256 == "" {
		writeError(w, http.StatusBadRequest, "missing_sha256", "sha256 is required")
		return
	}
	if err := s.launches.UploadAllocationFileRef(r.Context(), id, allocType, req.URL, req.SHA256, callerAddr); err != nil {
		writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, allocationUploadResponse{SHA256: req.SHA256})
}

// handleAllocationUploadBytes handles host-mode (Option C) uploads. It reuses the
// genesis host-mode flag and size cap — there is no separate allocation config.
func (s *Server) handleAllocationUploadBytes(w http.ResponseWriter, r *http.Request, id uuid.UUID, allocType, callerAddr string) {
	if !s.genesisHostMode {
		writeError(w, http.StatusBadRequest, "host_mode_disabled",
			"raw allocation file uploads are disabled; use attestor mode: "+
				"POST with Content-Type application/json and body {\"url\":\"...\",\"sha256\":\"...\"}")
		return
	}

	lr := io.LimitReader(r.Body, s.genesisMaxBytes+1)
	data, err := io.ReadAll(lr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "read_error", "could not read request body")
		return
	}
	if int64(len(data)) > s.genesisMaxBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "too_large",
			"allocation file exceeds the maximum allowed size")
		return
	}
	if len(data) == 0 {
		writeError(w, http.StatusBadRequest, "empty_body", "allocation file must not be empty")
		return
	}

	hash, err := s.launches.UploadAllocationFileBytes(r.Context(), id, allocType, data, callerAddr)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, allocationUploadResponse{SHA256: hash})
}

// GET /launch/{id}/allocations
//
// Lists the launch's curated allocation files (governance metadata only).
//
// @Summary      List allocation files
// @Tags         allocations
// @Produce      json
// @Param        id   path      string  true  "Launch UUID"
// @Success      200  {object}  allocationListResponse
// @Failure      400  {object}  errorEnvelope
// @Failure      404  {object}  errorEnvelope
// @Router       /launch/{id}/allocations [get]
func (s *Server) handleAllocationList(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "launch id must be a valid UUID")
		return
	}

	callerAddr := operatorFromContext(r.Context())
	l, err := s.launches.GetLaunch(r.Context(), id, callerAddr)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}

	out := make([]allocationFileJSON, 0, len(l.AllocationFiles))
	for _, f := range l.AllocationFiles {
		item := allocationFileJSON{
			Type:       string(f.Type),
			SHA256:     f.SHA256,
			Status:     string(f.Status),
			UploadedAt: f.UploadedAt,
		}
		if f.ApprovedByProposal != nil {
			pid := f.ApprovedByProposal.String()
			item.ApprovedByProposal = &pid
		}
		out = append(out, item)
	}
	writeJSON(w, http.StatusOK, allocationListResponse{Allocations: out})
}

// GET /launch/{id}/allocations/{type}
//
// Returns the allocation file of the given type: a 302 redirect (attestor mode) or
// the raw bytes (host mode), mirroring the genesis download.
//
// @Summary      Download an allocation file
// @Description  Returns 302 redirect (attestor mode) or streams the raw opaque file bytes (host mode).
// @Tags         allocations
// @Produce      application/octet-stream
// @Param        id    path      string  true  "Launch UUID"
// @Param        type  path      string  true  "Allocation type" Enums(accounts,claims,grants,authz,feegrant)
// @Success      200   {string}  string  "Raw allocation file bytes (host mode)"
// @Success      302   {string}  string  "Redirect to external URL (attestor mode)"
// @Failure      400   {object}  errorEnvelope
// @Failure      404   {object}  errorEnvelope
// @Router       /launch/{id}/allocations/{type} [get]
func (s *Server) handleAllocationGet(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "launch id must be a valid UUID")
		return
	}
	allocType := chi.URLParam(r, "type")

	// Visibility gate (ALLOWLIST launches are invisible to non-members → 404).
	callerAddr := operatorFromContext(r.Context())
	if _, err := s.launches.GetLaunch(r.Context(), id, callerAddr); err != nil {
		writeServiceError(w, r, err)
		return
	}

	ref, err := s.allocationStore.GetRef(r.Context(), id.String(), allocType)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}

	switch {
	case ref.ExternalURL != "":
		http.Redirect(w, r, ref.ExternalURL, http.StatusFound)
	case ref.LocalPath != "":
		f, err := os.Open(ref.LocalPath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				writeError(w, http.StatusNotFound, "not_found", "allocation file not found on disk")
				return
			}
			writeServiceError(w, r, err)
			return
		}
		defer f.Close()
		// Content is opaque (gentool CSV/TSV); coordd does not interpret it.
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.Copy(w, f)
	default:
		writeServiceError(w, r, errors.New("allocation store returned empty ref"))
	}
}
