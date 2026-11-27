//go:build !race

package ai

import (
	"bytes"
	"runtime"
	"testing"
)

// builtinCatalogEncodeCost encodes every built-in model as an extension host's model catalog does at attach, from a cold record cache, and returns the bytes
// allocated by the encoding and the heap bytes still live after it with the encodings dropped.
func builtinCatalogEncodeCost(t *testing.T) (allocated, retained uint64) {
	t.Helper()
	// µPi: the Melious models are added to the built-in list without a catalog record; their encoding falls back to member-by-member
	// (CatalogModelJSON ok=false), so only the models that have a record are measured here.
	var generated []GeneratedModel
	for i := range GeneratedModels {
		if ResolveCatalogRecord(GeneratedModels[i].Record) != "" {
			generated = append(generated, GeneratedModels[i])
		}
	}
	models := make([]*Model, len(generated))
	for i := range generated {
		models[i] = generated[i].ToModel()
	}
	clearCatalogRecordBaselines()
	runtime.GC()
	runtime.GC()
	var before, after, final runtime.MemStats
	runtime.ReadMemStats(&before)
	encoded := make([][]byte, len(models))
	for i, model := range models {
		data, ok, err := CatalogModelJSON(model)
		if err != nil || !ok {
			t.Fatalf("%s/%s: ok %t, %v", generated[i].Provider, generated[i].ID, ok, err)
		}
		encoded[i] = data
	}
	runtime.ReadMemStats(&after)
	for i := range encoded {
		if !bytes.Equal(encoded[i], []byte(ResolveCatalogRecord(generated[i].Record))) {
			t.Fatalf("model %d: encoding is not its catalog record", i)
		}
	}
	runtime.GC()
	runtime.GC()
	runtime.ReadMemStats(&final)
	runtime.KeepAlive(models)
	return after.TotalAlloc - before.TotalAlloc, final.HeapAlloc - min(final.HeapAlloc, before.HeapAlloc)
}

// An extension host sends the whole built-in catalog once at attach, so every built-in model is encoded in one burst at startup. An unmodified built-in
// model's encoding is its catalog record: the cost of proving it unmodified must not be a field-by-field decode of its record into ordered members, and
// the record cache must keep one small baseline per record, not a decoded shape. Both counts were the startup heap's largest transient and retained cost
// in a process that loads a TypeScript extension.
func TestBuiltinCatalogEncodeCost(t *testing.T) {
	allocated, retained := builtinCatalogEncodeCost(t)
	t.Logf("allocated %d B, retained %d B for %d models", allocated, retained, len(GeneratedModels))
	if limit := uint64(len(GeneratedModels)) * 10000; allocated > limit {
		t.Errorf("encoding the built-in catalog allocated %d B, want at most %d", allocated, limit)
	}
	if limit := uint64(len(GeneratedModels)) * 1000; retained > limit {
		t.Errorf("the record cache retains %d B after the built-in catalog, want at most %d", retained, limit)
	}
}
