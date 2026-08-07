//go:build contract

package contracts_test

import (
	"context"
	"testing"
	"time"

	"github.com/wangshangbin/homestack/internal/components"
)

func TestFixedComponentBinaries(t *testing.T) {
	for _, spec := range components.FixedSpecs {
		spec := spec
		t.Run(spec.ID, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if err := components.RequireVersion(ctx, spec); err != nil {
				t.Fatal(err)
			}
		})
	}
}
