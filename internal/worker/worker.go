package worker

import (
	"log"
	"time"

	"arcticexpress/internal/app"
	"arcticexpress/internal/store"
)

// Config controls worker timing and business-rule thresholds.
type Config struct {
	ReleaseInterval  time.Duration // how often to scan for expired reservations
	AnomalyInterval  time.Duration // how often to scan for temperature anomalies
	SpaceTimeout     time.Duration // 2-hour reservation confirmation window
	AnomalyThreshold time.Duration // 15-minute temperature excursion threshold
}

// DefaultConfig returns production-aligned thresholds.
func DefaultConfig() Config {
	return Config{
		ReleaseInterval:  1 * time.Minute,
		AnomalyInterval:  1 * time.Minute,
		SpaceTimeout:     2 * time.Hour,
		AnomalyThreshold: 15 * time.Minute,
	}
}

// Worker runs background tasks: auto-release of expired space reservations
// and temperature-anomaly detection.
type Worker struct {
	cfg     Config
	service *app.Service
	store   *store.Store
	stop    chan struct{}
	done    chan struct{}
}

// New creates a Worker wired to the given service.
func New(svc *app.Service, cfg Config) *Worker {
	return &Worker{
		cfg:     cfg,
		service: svc,
		store:   svc.GetStore(),
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}
}

// Start launches the two background loops.
func (w *Worker) Start() {
	go w.runReleaseLoop()
	go w.runAnomalyLoop()
}

// Stop signals both loops to exit and waits for them.
func (w *Worker) Stop() {
	close(w.stop)
	<-w.done
}

// RunReleaseOnce performs a single pass of the expired-reservation scan.
// Extracted so tests can call it deterministically.
func (w *Worker) RunReleaseOnce(now time.Time) int {
	expired := w.store.FindExpiredReservations(w.cfg.SpaceTimeout, now)
	for _, spaceID := range expired {
		sp, err := w.store.GetSpace(spaceID)
		if err != nil {
			continue
		}
		if sp.ReservedBy != "" {
			_ = w.store.RollbackBooking(sp.ReservedBy, now)
		} else {
			_ = w.store.ReleaseSpace(spaceID)
		}
	}
	return len(expired)
}

// RunAnomalyOnce performs a single pass of the temperature-anomaly scan.
func (w *Worker) RunAnomalyOnce(now time.Time) int {
	streaks := w.store.FindExpiredStreaks(w.cfg.AnomalyThreshold, now)
	for _, st := range streaks {
		anomaly := w.service.CreateAnomaly(st.ContainerID, st.BookingID, st.Since)
		w.store.SaveAnomaly(anomaly)
	}
	return len(streaks)
}

func (w *Worker) runReleaseLoop() {
	ticker := time.NewTicker(w.cfg.ReleaseInterval)
	defer ticker.Stop()
	for {
		select {
		case <-w.stop:
			w.done <- struct{}{}
			return
		case <-ticker.C:
			n := w.RunReleaseOnce(time.Now())
			if n > 0 {
				log.Printf("auto-released %d expired space reservations", n)
			}
		}
	}
}

func (w *Worker) runAnomalyLoop() {
	ticker := time.NewTicker(w.cfg.AnomalyInterval)
	defer ticker.Stop()
	for {
		select {
		case <-w.stop:
			return
		case <-ticker.C:
			n := w.RunAnomalyOnce(time.Now())
			if n > 0 {
				log.Printf("detected %d temperature anomalies", n)
			}
		}
	}
}
