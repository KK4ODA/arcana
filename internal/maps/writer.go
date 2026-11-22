package maps

import (
	"errors"
	"io"
	"os"
	"sort"
)

// WriteArchive assembles a PMTiles file from a header template, metadata
// (already compressed with the header's internal compression), sorted tile
// entries whose offsets point into dataFile, and dataFile itself.
//
// Layout: header, root directory, metadata, leaf directories, tile data.
// Entries must be sorted by tile ID; consecutive IDs with the same content
// are run-length encoded here.
func WriteArchive(out *os.File, tmpl Header, metadata []byte, entries []Entry, data io.Reader, dataLen uint64) error {
	if !sort.SliceIsSorted(entries, func(i, j int) bool { return entries[i].TileID < entries[j].TileID }) {
		return errors.New("entries not sorted")
	}
	entries = runLength(entries)
	if _, err := out.Write(assemblePrefix(tmpl, metadata, entries, dataLen)); err != nil {
		return err
	}
	n, err := io.Copy(out, data)
	if err != nil {
		return err
	}
	if uint64(n) != dataLen {
		return errors.New("tile data length mismatch")
	}
	return out.Sync()
}

// assemblePrefix builds the header, root directory, metadata and leaf
// directories for run-length-encoded entries over dataLen bytes of tiles.
func assemblePrefix(tmpl Header, metadata []byte, entries []Entry, dataLen uint64) []byte {
	root, leaves := BuildDirectories(entries)
	h := tmpl
	h.InternalCompression = CompressionGzip
	h.RootOffset = HeaderSize
	h.RootLength = uint64(len(root))
	h.MetadataOffset = h.RootOffset + h.RootLength
	h.MetadataLength = uint64(len(metadata))
	h.LeafOffset = h.MetadataOffset + h.MetadataLength
	h.LeafLength = uint64(len(leaves))
	h.DataOffset = h.LeafOffset + h.LeafLength
	h.DataLength = dataLen
	h.TileEntries = uint64(len(entries))
	contents := map[uint64]bool{}
	for _, e := range entries {
		h.AddressedTiles += uint64(e.RunLength)
		contents[e.Offset] = true
	}
	h.TileContents = uint64(len(contents))
	out := append(h.Bytes(), root...)
	out = append(out, metadata...)
	return append(out, leaves...)
}

// runLength merges consecutive tile IDs that share content.
func runLength(in []Entry) []Entry {
	var out []Entry
	for _, e := range in {
		if e.RunLength == 0 {
			e.RunLength = 1
		}
		if n := len(out); n > 0 {
			last := &out[n-1]
			if last.Offset == e.Offset && last.Length == e.Length && last.TileID+uint64(last.RunLength) == e.TileID {
				last.RunLength += e.RunLength
				continue
			}
		}
		out = append(out, e)
	}
	return out
}
