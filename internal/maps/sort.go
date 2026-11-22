package maps

import "sort"

func sortEntries(e []Entry) {
	sort.Slice(e, func(i, j int) bool { return e[i].TileID < e[j].TileID })
}
