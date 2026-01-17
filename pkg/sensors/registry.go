package sensors

import (
	"errors"
	"math"
	"strings"
	"sync"
)

var (
	ErrEmptyName    = errors.New("name must not be empty")
	ErrInvalidValue = errors.New("value must be a finite number")
)

type Entry struct {
	Name  string
	Value float64
}

type Registry struct {
	mu         sync.RWMutex
	values     map[string]float64
	cachedList []Entry
}

func NewRegistry() *Registry {
	return &Registry{
		values: make(map[string]float64),
	}
}

func (r *Registry) Set(name string, value float64) error {
	if err := validateName(name); err != nil {
		return err
	}

	if !isFinite(value) {
		return ErrInvalidValue
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.values[name] = value
	r.cachedList = nil
	return nil
}

func (r *Registry) Get(name string) (float64, bool, error) {
	if err := validateName(name); err != nil {
		return 0, false, err
	}

	r.mu.RLock()
	defer r.mu.RUnlock()
	value, ok := r.values[name]
	return value, ok, nil
}

func (r *Registry) Delete(name string) error {
	if err := validateName(name); err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.values, name)
	r.cachedList = nil
	return nil
}

func (r *Registry) List() []Entry {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.cachedList == nil {
		r.cachedList = make([]Entry, 0, len(r.values))
		for name, value := range r.values {
			r.cachedList = append(r.cachedList, Entry{
				Name:  name,
				Value: value,
			})
		}
	}

	return r.cachedList
}

func validateName(name string) error {
	if strings.TrimSpace(name) == "" {
		return ErrEmptyName
	}
	return nil
}

func isFinite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
