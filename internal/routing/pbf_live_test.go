package routing

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestReadRealPBF reads a real extract named by ARCANA_TEST_PBF.
func TestReadRealPBF(t *testing.T) {
	p := os.Getenv("ARCANA_TEST_PBF")
	if p == "" {
		t.Skip("set ARCANA_TEST_PBF to an .osm.pbf file")
	}
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	start := time.Now()
	var nodes, tagged, ways, rels, highways int
	err = ReadPBF(context.Background(), f, Handlers{
		Nodes:    func(b *NodeBatch) { nodes += len(b.IDs); tagged += len(b.Tags) },
		NodeTags: true,
		Way: func(w *Way) {
			ways++
			if tagValue(w.Tags, "highway") != "" {
				highways++
			}
		},
		Relation: func(r *Relation) { rels++ },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d nodes (%d tagged), %d ways (%d highways), %d relations in %s", nodes, tagged, ways, highways, rels, time.Since(start))
	if nodes == 0 || ways == 0 {
		t.Fatal("nothing read")
	}
}
