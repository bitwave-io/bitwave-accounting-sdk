package store_test

import (
	"testing"

	"github.com/bitwave-io/bitwave-accounting-sdk/store"
)

func TestMemoryStore_Contract(t *testing.T) {
	runStoreContract(t, func(t *testing.T, name, base string) store.Store {
		return store.NewMemory(name, base)
	})
}
