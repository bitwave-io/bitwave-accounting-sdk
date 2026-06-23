package store_test

import (
	"testing"

	"github.com/bitwave-io/bitwave-accounting-sdk/store"
)

func TestLocalStore_Contract(t *testing.T) {
	runStoreContract(t, func(t *testing.T, name, base string) store.Store {
		dir := t.TempDir()
		s, err := store.InitLocal(dir, name, base)
		if err != nil {
			t.Fatalf("InitLocal: %v", err)
		}
		return s
	})
}
