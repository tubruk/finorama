package vault

import (
	"bytes"
	"testing"
)

func TestVaultInternalZeroing(t *testing.T) {
	v := New()
	sampleKey := []byte("01234567890123456789012345678901")
	v.Unlock(sampleKey)

	// Keep reference to the internal slice backing array
	internalSlice := v.key
	if len(internalSlice) != len(sampleKey) {
		t.Fatalf("expected internal slice length %d, got %d", len(sampleKey), len(internalSlice))
	}

	v.Lock()

	// Verify internalSlice was zeroed
	expectedZeroes := make([]byte, len(sampleKey))
	if !bytes.Equal(internalSlice, expectedZeroes) {
		t.Fatalf("expected internal slice to be zeroed out on Lock(), got %v", internalSlice)
	}

	if v.key != nil {
		t.Fatalf("expected v.key to be nil after Lock()")
	}
}
