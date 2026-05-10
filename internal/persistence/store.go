package persistence

import "errors"

var ErrNotFound = errors.New("state not found")

type Store interface {
	Load(key string, target any) error
	Save(key string, value any) error
}

type noopStore struct{}

func NewNoopStore() Store {
	return noopStore{}
}

func (noopStore) Load(_ string, _ any) error {
	return ErrNotFound
}

func (noopStore) Save(_ string, _ any) error {
	return nil
}
