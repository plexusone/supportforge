package memstore_test

import (
	"testing"

	"github.com/plexusone/supportforge/store"
	"github.com/plexusone/supportforge/store/memstore"
	"github.com/plexusone/supportforge/store/storetest"
)

func TestConformance(t *testing.T) {
	storetest.Run(t, func(*testing.T) store.Store { return memstore.New() })
}
