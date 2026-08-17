package httpapi

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"

	"arcticexpress/internal/app"
	"arcticexpress/internal/domain"
	"arcticexpress/internal/store"
)

// Handler exposes the booking orchestration via REST.
type Handler struct {
	svc *app.Service
}

// NewHandler creates an HTTP handler backed by the given service.
func NewHandler(svc *app.Service) *Handler {
	return &Handler{svc: svc}
}

// Register wires all routes onto the given mux.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/bookings", h.submitBooking)
	mux.HandleFunc("GET /api/bookings", h.listBookings)
	mux.HandleFunc("GET /api/bookings/{id}", h.getBooking)
	mux.HandleFunc("POST /api/bookings/{id}/confirm-space", h.confirmSpace)
	mux.HandleFunc("POST /api/bookings/{id}/review-dg", h.reviewDG)
	mux.HandleFunc("POST /api/bookings/{id}/configure-temp", h.configureTemp)
	mux.HandleFunc("POST /api/bookings/{id}/load", h.loadCargo)
	mux.HandleFunc("POST /api/bookings/{id}/depart", h.departVessel)
	mux.HandleFunc("POST /api/bookings/{id}/temperature", h.recordTemperature)
	mux.HandleFunc("POST /api/bookings/{id}/arrive", h.arrive)
	mux.HandleFunc("POST /api/bookings/{id}/customs", h.clearCustoms)
	mux.HandleFunc("POST /api/bookings/{id}/deliver", h.deliver)
	mux.HandleFunc("POST /api/bookings/{id}/port-change", h.portChange)
	mux.HandleFunc("POST /api/bookings/{id}/roll", h.rollCargo)
	mux.HandleFunc("POST /api/bookings/{id}/equipment-failure", h.equipmentFailure)
	mux.HandleFunc("GET /api/bookings/{id}/temperature-curve", h.temperatureCurve)
}

// --- request/response types ---

type submitBookingReq struct {
	ForwarderID string  `json:"forwarder_id"`
	VoyageID    string  `json:"voyage_id"`
	CargoType   string  `json:"cargo_type"`
	Description string  `json:"description"`
	LoadType    string  `json:"load_type"`
	WeightKg    float64 `json:"weight_kg"`
	ContainerID string  `json:"container_id"`
	SpaceID     string  `json:"space_id"`
}

type configureTempReq struct {
	ContainerID string  `json:"container_id"`
	MinTempC    float64 `json:"min_temp_c"`
	MaxTempC    float64 `json:"max_temp_c"`
	SetTempC    float64 `json:"set_temp_c"`
}

type recordTempReq struct {
	ContainerID string  `json:"container_id"`
	TempC       float64 `json:"temp_c"`
	Stage       string  `json:"stage"`
}

type portChangeReq struct {
	NewPort string `json:"new_port"`
}

type rollCargoReq struct {
	NewSpaceID string `json:"new_space_id"`
}

// --- handlers ---

func (h *Handler) submitBooking(w http.ResponseWriter, r *http.Request) {
	var req submitBookingReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	b, err := h.svc.SubmitBooking(app.SubmitBookingRequest{
		ForwarderID: req.ForwarderID,
		VoyageID:    req.VoyageID,
		CargoType:   req.CargoType,
		Description: req.Description,
		LoadType:    req.LoadType,
		WeightKg:    req.WeightKg,
		ContainerID: req.ContainerID,
		SpaceID:     req.SpaceID,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, b)
}

func (h *Handler) getBooking(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	b, err := h.svc.GetBooking(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "booking not found")
		return
	}
	writeJSON(w, http.StatusOK, b)
}

func (h *Handler) listBookings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.svc.GetStore().ListBookings())
}

func (h *Handler) confirmSpace(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.svc.ConfirmSpace(id); err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "space_confirmed"})
}

func (h *Handler) reviewDG(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.svc.ApproveDG(id); err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "dg_reviewed"})
}

func (h *Handler) configureTemp(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req configureTempReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := h.svc.ConfigureTemp(id, req.ContainerID, req.MinTempC, req.MaxTempC, req.SetTempC); err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "temp_configured"})
}

func (h *Handler) loadCargo(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.svc.LoadCargo(id); err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "loaded"})
}

func (h *Handler) departVessel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.svc.DepartVessel(id); err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "in_transit"})
}

func (h *Handler) recordTemperature(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req recordTempReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	outOfRange, err := h.svc.RecordTemperature(id, req.ContainerID, req.TempC, domain.TempStage(req.Stage))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":       "recorded",
		"out_of_range": outOfRange,
	})
}

func (h *Handler) arrive(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.svc.ArriveAtDestination(id); err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "arrived"})
}

func (h *Handler) clearCustoms(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.svc.ClearCustoms(id); err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "customs_cleared"})
}

func (h *Handler) deliver(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.svc.Deliver(id); err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "delivered"})
}

func (h *Handler) portChange(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req portChangeReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := h.svc.RequestPortChange(id, req.NewPort); err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "port_change_requested"})
}

func (h *Handler) rollCargo(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req rollCargoReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := h.svc.HandleRollCargo(id, req.NewSpaceID); err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "rolled_to_next_voyage"})
}

func (h *Handler) equipmentFailure(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ef, err := h.svc.HandleEquipmentFailure(id)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ef)
}

func (h *Handler) temperatureCurve(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	b, err := h.svc.GetBooking(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "booking not found")
		return
	}
	curve := h.svc.GetStore().ListTempReadings(b.ContainerID)
	writeJSON(w, http.StatusOK, curve)
}

// --- helpers ---

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func writeDomainError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, domain.ErrBatteryMustBeFCL):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, domain.ErrPortChangeTooLate):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, domain.ErrInvalidTransition):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, domain.ErrFreeStorageExpired):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, store.ErrContainerOccupied):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, store.ErrSpaceUnavailable):
		writeError(w, http.StatusConflict, err.Error())
	default:
		if strings.Contains(err.Error(), "reserve space") || strings.Contains(err.Error(), "allocate container") {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		log.Printf("unhandled error: %v", err)
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}
