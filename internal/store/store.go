package store

import (
	"errors"
	"sync"
	"time"

	"arcticexpress/internal/domain"
)

var (
	ErrNotFound          = errors.New("not found")
	ErrContainerOccupied = errors.New("container is occupied by another booking with earlier declaration")
	ErrSpaceUnavailable  = errors.New("space is not available for reservation")
	ErrSpaceNotReserved  = errors.New("space is not reserved by this booking")
)

// containerAlloc tracks which booking holds a reefer container.
type containerAlloc struct {
	bookingID  string
	declaredAt time.Time
}

// tempStreak tracks the start of a continuous out-of-range period.
type tempStreak struct {
	containerID string
	bookingID   string
	since       time.Time
}

// TempStreakInfo is the exported view of a continuous out-of-range period.
type TempStreakInfo struct {
	ContainerID string
	BookingID   string
	Since       time.Time
}

// Store is a concurrency-safe in-memory persistence layer.
type Store struct {
	mu               sync.Mutex
	bookings         map[string]*domain.Booking
	spaces           map[string]*domain.Space
	voyages          map[string]*domain.Voyage
	tempConfigs      map[string]*domain.TempConfig
	tempReadings     map[string][]domain.TempReading
	anomalies        map[string]*domain.Anomaly
	equipmentFailure map[string]*domain.EquipmentFailure
	containers       map[string]*containerAlloc
	tempStreaks      map[string]*tempStreak
}

// New creates an empty store.
func New() *Store {
	return &Store{
		bookings:         make(map[string]*domain.Booking),
		spaces:           make(map[string]*domain.Space),
		voyages:          make(map[string]*domain.Voyage),
		tempConfigs:      make(map[string]*domain.TempConfig),
		tempReadings:     make(map[string][]domain.TempReading),
		anomalies:        make(map[string]*domain.Anomaly),
		equipmentFailure: make(map[string]*domain.EquipmentFailure),
		containers:       make(map[string]*containerAlloc),
		tempStreaks:      make(map[string]*tempStreak),
	}
}

// --- Booking operations ---

func (s *Store) SaveBooking(b *domain.Booking) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bookings[b.ID] = b
}

func (s *Store) GetBooking(id string) (*domain.Booking, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.bookings[id]
	if !ok {
		return nil, ErrNotFound
	}
	cp := *b
	return &cp, nil
}

func (s *Store) ListBookings() []domain.Booking {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]domain.Booking, 0, len(s.bookings))
	for _, b := range s.bookings {
		out = append(out, *b)
	}
	return out
}

// MutateBooking applies fn to the booking under the store lock so that
// domain transitions and side-effect updates are atomic.
func (s *Store) MutateBooking(id string, fn func(b *domain.Booking) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.bookings[id]
	if !ok {
		return ErrNotFound
	}
	return fn(b)
}

// --- Space operations ---

func (s *Store) SaveSpace(sp *domain.Space) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.spaces[sp.ID] = sp
}

func (s *Store) GetSpace(id string) (*domain.Space, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sp, ok := s.spaces[id]
	if !ok {
		return nil, ErrNotFound
	}
	cp := *sp
	return &cp, nil
}

// ReserveSpace atomically transitions an available space to reserved.
func (s *Store) ReserveSpace(spaceID, bookingID string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sp, ok := s.spaces[spaceID]
	if !ok {
		return ErrNotFound
	}
	if sp.Status != domain.SpaceStatusAvailable {
		return ErrSpaceUnavailable
	}
	sp.Status = domain.SpaceStatusReserved
	sp.ReservedAt = &now
	sp.ReservedBy = bookingID
	return nil
}

// ConfirmSpace atomically transitions a reserved space to confirmed.
func (s *Store) ConfirmSpace(spaceID, bookingID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sp, ok := s.spaces[spaceID]
	if !ok {
		return ErrNotFound
	}
	if sp.Status != domain.SpaceStatusReserved || sp.ReservedBy != bookingID {
		return ErrSpaceNotReserved
	}
	sp.Status = domain.SpaceStatusConfirmed
	sp.ConfirmedBy = bookingID
	return nil
}

// ReleaseSpace returns a space to the pool and clears reservation metadata.
func (s *Store) ReleaseSpace(spaceID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sp, ok := s.spaces[spaceID]
	if !ok {
		return ErrNotFound
	}
	sp.Status = domain.SpaceStatusAvailable
	sp.ReservedAt = nil
	sp.ReservedBy = ""
	sp.ConfirmedBy = ""
	return nil
}

// FindExpiredReservations returns space IDs reserved longer than timeout ago
// that have not been confirmed.
func (s *Store) FindExpiredReservations(timeout time.Duration, now time.Time) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var expired []string
	for _, sp := range s.spaces {
		if sp.Status == domain.SpaceStatusReserved && sp.ReservedAt != nil {
			if now.Sub(*sp.ReservedAt) >= timeout {
				expired = append(expired, sp.ID)
			}
		}
	}
	return expired
}

// --- Voyage operations ---

func (s *Store) SaveVoyage(v *domain.Voyage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.voyages[v.ID] = v
}

func (s *Store) GetVoyage(id string) (*domain.Voyage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.voyages[id]
	if !ok {
		return nil, ErrNotFound
	}
	cp := *v
	return &cp, nil
}

func (s *Store) MarkVoyageDeparted(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.voyages[id]
	if !ok {
		return ErrNotFound
	}
	v.Departed = true
	return nil
}

// --- Container allocation with conflict resolution ---

// AllocateContainer assigns a reefer container to a booking. If the container
// is already held by another booking, the earlier declaration wins; the loser
// is returned so the caller can roll it back.
func (s *Store) AllocateContainer(containerID, bookingID string, declaredAt time.Time) (loserBookingID string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	existing, ok := s.containers[containerID]
	if !ok {
		s.containers[containerID] = &containerAlloc{bookingID: bookingID, declaredAt: declaredAt}
		return "", nil
	}
	if existing.bookingID == bookingID {
		return "", nil
	}

	if declaredAt.Before(existing.declaredAt) {
		loser := existing.bookingID
		s.containers[containerID] = &containerAlloc{bookingID: bookingID, declaredAt: declaredAt}
		return loser, nil
	}

	return "", ErrContainerOccupied
}

// ReleaseContainer frees a container allocation so it can be reused.
func (s *Store) ReleaseContainer(containerID, bookingID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if a, ok := s.containers[containerID]; ok && a.bookingID == bookingID {
		delete(s.containers, containerID)
	}
}

// ReassignContainer moves a container allocation from one booking to another
// (used during backup-container reassignment).
func (s *Store) ReassignContainer(containerID, oldBookingID, newBookingID string, declaredAt time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.containers[containerID] = &containerAlloc{bookingID: newBookingID, declaredAt: declaredAt}
	_ = oldBookingID
}

// --- Temperature config & readings ---

func (s *Store) SaveTempConfig(tc *domain.TempConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tempConfigs[tc.BookingID] = tc
}

func (s *Store) GetTempConfig(bookingID string) (*domain.TempConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tc, ok := s.tempConfigs[bookingID]
	if !ok {
		return nil, ErrNotFound
	}
	cp := *tc
	return &cp, nil
}

// AddTempReading stores a reading and updates the out-of-range streak state.
// It returns whether the reading was out of range.
func (s *Store) AddTempReading(r domain.TempReading, cfg *domain.TempConfig) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tempReadings[r.ContainerID] = append(s.tempReadings[r.ContainerID], r)
	inRange := cfg.IsWithinRange(r.TempC)
	if !inRange {
		if _, ok := s.tempStreaks[r.ContainerID]; !ok {
			s.tempStreaks[r.ContainerID] = &tempStreak{
				containerID: r.ContainerID,
				bookingID:   r.BookingID,
				since:       r.RecordedAt,
			}
		}
	} else {
		delete(s.tempStreaks, r.ContainerID)
	}
	return !inRange
}

// ListTempReadings returns all readings for a container, ordered by time.
func (s *Store) ListTempReadings(containerID string) []domain.TempReading {
	s.mu.Lock()
	defer s.mu.Unlock()
	src := s.tempReadings[containerID]
	out := make([]domain.TempReading, len(src))
	copy(out, src)
	return out
}

// CarryOverTempReadings hands the recorded curve of a failed reefer over to the
// backup container so the temperature history stays continuous for claims. The
// carried entries are re-stamped with the backup container ID and returned count
// is the number of entries handed over.
func (s *Store) CarryOverTempReadings(fromContainerID, toContainerID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	carried := s.tempReadings[fromContainerID]
	for i := range carried {
		carried[i].ContainerID = toContainerID
	}
	s.tempReadings[toContainerID] = append(s.tempReadings[toContainerID], carried...)
	return len(carried)
}

// FindExpiredStreaks returns containers whose out-of-range streak exceeds the
// threshold and for which no open anomaly has been created yet.
func (s *Store) FindExpiredStreaks(threshold time.Duration, now time.Time) []TempStreakInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	var expired []TempStreakInfo
	for _, st := range s.tempStreaks {
		if now.Sub(st.since) >= threshold {
			if a, ok := s.anomalies[st.containerID]; ok && a.Status == domain.AnomalyStatusOpen {
				continue
			}
			expired = append(expired, TempStreakInfo{
				ContainerID: st.containerID,
				BookingID:   st.bookingID,
				Since:       st.since,
			})
		}
	}
	return expired
}

// --- Anomaly operations ---

func (s *Store) SaveAnomaly(a *domain.Anomaly) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.anomalies[a.ContainerID] = a
}

func (s *Store) GetAnomaly(containerID string) (*domain.Anomaly, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.anomalies[containerID]
	if !ok {
		return nil, ErrNotFound
	}
	cp := *a
	return &cp, nil
}

// --- Equipment failure operations ---

func (s *Store) SaveEquipmentFailure(ef *domain.EquipmentFailure) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.equipmentFailure[ef.ContainerID] = ef
}

func (s *Store) GetEquipmentFailure(containerID string) (*domain.EquipmentFailure, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ef, ok := s.equipmentFailure[containerID]
	if !ok {
		return nil, ErrNotFound
	}
	cp := *ef
	return &cp, nil
}

// ConfirmBookingSpace atomically confirms the booking's space flag and
// transitions the associated space to confirmed. Idempotent.
func (s *Store) ConfirmBookingSpace(bookingID string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.bookings[bookingID]
	if !ok {
		return ErrNotFound
	}
	if b.SpaceConfirmed {
		return nil
	}
	if err := b.ConfirmSpace(now); err != nil {
		return err
	}
	if sp, ok := s.spaces[b.SpaceID]; ok {
		sp.Status = domain.SpaceStatusConfirmed
		sp.ConfirmedBy = bookingID
	}
	return nil
}

// RollbackBooking cancels a booking and releases its space and container
// allocation atomically. Used when a booking loses a container conflict
// or when its space reservation expires.
func (s *Store) RollbackBooking(bookingID string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.bookings[bookingID]
	if !ok {
		return ErrNotFound
	}
	if a, ok := s.containers[b.ContainerID]; ok && a.bookingID == bookingID {
		delete(s.containers, b.ContainerID)
	}
	if sp, ok := s.spaces[b.SpaceID]; ok {
		sp.Status = domain.SpaceStatusAvailable
		sp.ReservedAt = nil
		sp.ReservedBy = ""
		sp.ConfirmedBy = ""
	}
	_ = b.Cancel(now)
	return nil
}

// FindAvailableReeferSpace returns the first available reefer space on the
// given voyage, useful for backup-container reassignment.
func (s *Store) FindAvailableReeferSpace(voyageID string) (*domain.Space, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sp := range s.spaces {
		if sp.VoyageID == voyageID && sp.Reefer && sp.Status == domain.SpaceStatusAvailable {
			cp := *sp
			return &cp, nil
		}
	}
	return nil, ErrNotFound
}
