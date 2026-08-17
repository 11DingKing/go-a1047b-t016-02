package main

import (
	"log"
	"net/http"
	"os"
	"time"

	"arcticexpress/internal/app"
	"arcticexpress/internal/domain"
	"arcticexpress/internal/httpapi"
	"arcticexpress/internal/store"
	"arcticexpress/internal/worker"
)

func main() {
	port := envOr("PORT", "56282")

	st := store.New()
	svc := app.NewService(st)
	seedDemoData(svc)

	wkr := worker.New(svc, worker.DefaultConfig())
	wkr.Start()
	defer wkr.Stop()

	mux := http.NewServeMux()
	h := httpapi.NewHandler(svc)
	h.Register(mux)

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	addr := ":" + port
	log.Printf("arctic-express listening on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// seedDemoData creates two voyages and several spaces so the API is
// immediately usable without external setup.
func seedDemoData(svc *app.Service) {
	now := time.Now()

	voyage1 := &domain.Voyage{
		ID:            "VG-001",
		VesselID:      "VS-Arctic-Express",
		VesselName:    "Arctic Express",
		DeparturePort: "Shanghai",
		ArrivalPort:   "Rotterdam",
		DepartureAt:   now.Add(72 * time.Hour),
		ArrivalAt:     now.Add(35 * 24 * time.Hour),
		NextVoyageID:  "VG-002",
	}
	voyage2 := &domain.Voyage{
		ID:            "VG-002",
		VesselID:      "VS-Arctic-Express",
		VesselName:    "Arctic Express",
		DeparturePort: "Shanghai",
		ArrivalPort:   "Rotterdam",
		DepartureAt:   now.Add(7 * 24 * time.Hour),
		ArrivalAt:     now.Add(42 * 24 * time.Hour),
	}
	svc.SeedVoyage(voyage1)
	svc.SeedVoyage(voyage2)

	spaces := []*domain.Space{
		{ID: "SP-001", VesselID: "VS-Arctic-Express", VoyageID: "VG-001", ContainerType: "40HC", Reefer: true, Status: domain.SpaceStatusAvailable},
		{ID: "SP-002", VesselID: "VS-Arctic-Express", VoyageID: "VG-001", ContainerType: "40HC", Reefer: true, Status: domain.SpaceStatusAvailable},
		{ID: "SP-003", VesselID: "VS-Arctic-Express", VoyageID: "VG-001", ContainerType: "40GP", Reefer: false, Status: domain.SpaceStatusAvailable},
		{ID: "SP-004", VesselID: "VS-Arctic-Express", VoyageID: "VG-002", ContainerType: "40HC", Reefer: true, Status: domain.SpaceStatusAvailable},
		{ID: "SP-005", VesselID: "VS-Arctic-Express", VoyageID: "VG-002", ContainerType: "40GP", Reefer: false, Status: domain.SpaceStatusAvailable},
	}
	for _, sp := range spaces {
		svc.SeedSpace(sp)
	}
}
