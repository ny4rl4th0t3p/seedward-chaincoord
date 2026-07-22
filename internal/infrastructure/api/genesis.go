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

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/ports"
)

// Genesis type selectors used on both the upload (?type=) and download (?type=) paths.
const (
	genesisTypeInitial = "initial"
	genesisTypeFinal   = "final"
)

// genesisRefRequest is the JSON body for Option A (attestor mode) uploads.
// GenesisTime is required for final genesis refs and must be an RFC 3339 timestamp.
type genesisRefRequest struct {
	URL         string `json:"url"`
	SHA256      string `json:"sha256"`
	GenesisTime string `json:"genesis_time"`
}

// genesisUploadResponse is returned after a successful genesis upload/registration.
type genesisUploadResponse struct {
	SHA256 string `json:"sha256"`
}

// genesisHashResponse carries the current initial/final genesis hashes.
type genesisHashResponse struct {
	InitialSHA256 string `json:"initial_sha256"`
	FinalSHA256   string `json:"final_sha256"`
}

// POST /launch/{id}/genesis
//
// Accepts two modes based on Content-Type:
//
//   - application/json (default / attestor mode):
//     Body: {"url":"https://...","sha256":"<64-char hex>"}
//     Stores only the URL + hash reference; no bytes are persisted on this server.
//     Clients receive a 302 redirect to the external URL on GET.
//
//   - application/octet-stream (host mode, requires COORD_GENESIS_HOST_MODE=true):
//     Body: raw genesis JSON bytes (capped at GenesisMaxBytes).
//     Bytes are stored on disk and served directly on GET.
//     Returns 400 if host mode is not enabled.
//
// Use ?type=final for the post-gentx assembled genesis; omit (or pass ?type=initial)
// for the pre-gentx initial genesis.
//
// @Summary      Upload genesis file
// @Description  Committee members only.
// @Description  Attestor mode (default): register external URL + SHA-256.
// @Description  Host mode: upload raw bytes (requires COORD_GENESIS_HOST_MODE=true).
// @Tags         genesis
// @Security     BearerAuth
// @Accept       application/json
// @Accept       application/octet-stream
// @Produce      json
// @Param        id    path      string  true   "Launch UUID"
// @Param        type  query     string  false  "Genesis type" Enums(initial,final)
// @Success      200   {object}  genesisUploadResponse
// @Failure      400   {object}  errorEnvelope
// @Failure      401   {object}  errorEnvelope
// @Failure      403   {object}  errorEnvelope
// @Failure      413   {object}  errorEnvelope
// @Router       /launch/{id}/genesis [post]
func (s *Server) handleGenesisUpload(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "launch id must be a valid UUID")
		return
	}

	genesisType := r.URL.Query().Get("type") // "initial" (default) or "final"
	callerAddr := operatorFromContext(r.Context())

	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "application/json") {
		s.handleGenesisUploadRef(w, r, id, genesisType, callerAddr)
	} else {
		s.handleGenesisUploadBytes(w, r, id, genesisType, callerAddr)
	}
}

// handleGenesisUploadRef handles Option A (attestor mode) uploads.
func (s *Server) handleGenesisUploadRef(w http.ResponseWriter, r *http.Request, id uuid.UUID, genesisType, callerAddr string) {
	var req genesisRefRequest
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

	var err error
	if genesisType == genesisTypeFinal {
		if req.GenesisTime == "" {
			writeError(w, http.StatusBadRequest, "missing_genesis_time", "genesis_time is required for final genesis refs")
			return
		}
		gt, parseErr := time.Parse(time.RFC3339, req.GenesisTime)
		if parseErr != nil {
			writeError(w, http.StatusBadRequest, "invalid_genesis_time", "genesis_time must be an RFC 3339 timestamp (e.g. 2026-06-01T12:00:00Z)")
			return
		}
		err = s.launches.UploadFinalGenesisRef(r.Context(), id, req.URL, req.SHA256, gt, callerAddr)
	} else {
		err = s.launches.UploadInitialGenesisRef(r.Context(), id, req.URL, req.SHA256, callerAddr)
	}
	if err != nil {
		writeServiceError(w, r, err)
		return
	}

	writeJSON(w, http.StatusOK, genesisUploadResponse{SHA256: req.SHA256})
}

// handleGenesisUploadBytes handles Option C (host mode) uploads.
func (s *Server) handleGenesisUploadBytes(w http.ResponseWriter, r *http.Request, id uuid.UUID, genesisType, callerAddr string) {
	if !s.genesisHostMode {
		writeError(w, http.StatusBadRequest, "host_mode_disabled",
			"raw genesis file uploads are disabled; use attestor mode: "+
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
			"genesis file exceeds the maximum allowed size")
		return
	}
	if len(data) == 0 {
		writeError(w, http.StatusBadRequest, "empty_body", "genesis file must not be empty")
		return
	}

	var hash string
	if genesisType == genesisTypeFinal {
		hash, err = s.launches.UploadFinalGenesis(r.Context(), id, data, callerAddr)
	} else {
		hash, err = s.launches.UploadInitialGenesis(r.Context(), id, data, callerAddr)
	}
	if err != nil {
		writeServiceError(w, r, err)
		return
	}

	writeJSON(w, http.StatusOK, genesisUploadResponse{SHA256: hash})
}

// GET /launch/{id}/genesis
//
// Returns the genesis file for the launch. Behavior depends on how the
// genesis was registered:
//
//   - Option A (attestor): returns 302 Found with Location set to the
//     external URL. The client fetches the file directly from there.
//   - Option C (host): streams the file with Content-Type application/json.
//
// With no ?type, returns the final genesis if one has been registered; otherwise the
// initial genesis. ?type=initial|final selects a specific stored genesis — the initial
// stays downloadable after the final is published (reproduction anchor).
//
// @Summary      Download genesis file
// @Description  Returns 302 redirect (attestor mode) or streams raw genesis JSON (host mode).
// @Description  ?type=initial|final selects which stored genesis to serve. The initial stays
// @Description  downloadable after the final is published, so committee members can still
// @Description  reproduce the final from its inputs. Omit type for the current genesis
// @Description  (final once published, else initial).
// @Tags         genesis
// @Produce      json
// @Param        id    path      string  true   "Launch UUID"
// @Param        type  query     string  false  "Genesis type" Enums(initial,final)
// @Success      200  {string}  string  "Raw genesis JSON (host mode)"
// @Success      302  {string}  string  "Redirect to external genesis URL (attestor mode)"
// @Failure      400  {object}  errorEnvelope
// @Failure      404  {object}  errorEnvelope
// @Router       /launch/{id}/genesis [get]
func (s *Server) handleGenesisGet(w http.ResponseWriter, r *http.Request) {
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

	if l.InitialGenesisSHA256 == "" {
		writeError(w, http.StatusNotFound, "not_found", "no genesis file has been uploaded for this launch")
		return
	}

	// Resolve which stored ref to serve. type=initial always returns the retained initial (even
	// after the final is published — the reproduction/verification anchor for PUBLISH_GENESIS);
	// type=final returns the published final; no type returns the current genesis (final if
	// published, else initial) for back-compatibility.
	var gr *ports.StoredFileRef
	switch r.URL.Query().Get("type") {
	case genesisTypeInitial:
		gr, err = s.genesisStore.GetInitialRef(r.Context(), id.String())
	case genesisTypeFinal:
		if l.FinalGenesisSHA256 == "" {
			writeError(w, http.StatusNotFound, "not_found", "no final genesis has been published for this launch")
			return
		}
		gr, err = s.genesisStore.GetFinalRef(r.Context(), id.String())
	case "":
		if l.FinalGenesisSHA256 != "" {
			gr, err = s.genesisStore.GetFinalRef(r.Context(), id.String())
		} else {
			gr, err = s.genesisStore.GetInitialRef(r.Context(), id.String())
		}
	default:
		writeError(w, http.StatusBadRequest, "invalid_type", "type must be 'initial' or 'final'")
		return
	}
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	externalURL, localPath := gr.ExternalURL, gr.LocalPath

	switch {
	case externalURL != "":
		http.Redirect(w, r, externalURL, http.StatusFound)
	case localPath != "":
		f, err := os.Open(localPath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				writeError(w, http.StatusNotFound, "not_found", "genesis file not found on disk")
				return
			}
			writeServiceError(w, r, err)
			return
		}
		defer f.Close()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.Copy(w, f)
	default:
		writeServiceError(w, r, errors.New("genesis store returned empty ref"))
	}
}

// GET /launch/{id}/genesis/hash
// Returns the current genesis SHA256 hash(es).
// Response: { "initial_sha256": "...", "final_sha256": "..." }
//
// @Summary      Get genesis hashes
// @Tags         genesis
// @Produce      json
// @Param        id   path      string  true  "Launch UUID"
// @Success      200  {object}  genesisHashResponse
// @Failure      400  {object}  errorEnvelope
// @Failure      404  {object}  errorEnvelope
// @Router       /launch/{id}/genesis/hash [get]
func (s *Server) handleGenesisHashGet(w http.ResponseWriter, r *http.Request) {
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

	writeJSON(w, http.StatusOK, genesisHashResponse{
		InitialSHA256: l.InitialGenesisSHA256,
		FinalSHA256:   l.FinalGenesisSHA256,
	})
}
