package storage

import "fmt"

// PlanInput is everything needed to project how a library volume's space will
// be used. All values are bytes.
type PlanInput struct {
	Volume Volume
	// Installed is the size of verified, published resources.
	Installed int64
	// Staged is the size of partial downloads already written to disk.
	Staged int64
	// QueuedRemaining is what queued and running downloads still need to
	// write (expected size minus bytes already staged).
	QueuedRemaining int64
	// Selected is the size of resources the user is considering but has not
	// queued yet.
	Selected int64
	// SelectedUncertain is the part of Selected that comes from estimates
	// rather than exact sizes published by the source.
	SelectedUncertain int64
	// Reserve is the minimum free space the user wants kept on the drive.
	Reserve int64
}

// Plan is a projection of the library volume's capacity.
type Plan struct {
	Total     int64 `json:"total"`
	Available int64 `json:"available"`
	Installed int64 `json:"installed"`
	Staged    int64 `json:"staged"`
	// Other is space used by files that are not Arcana resources.
	Other             int64 `json:"other"`
	Queued            int64 `json:"queued"`
	Selected          int64 `json:"selected"`
	SelectedUncertain int64 `json:"selectedUncertain"`
	Reserve           int64 `json:"reserve"`
	// AfterQueue is free space once every queued download finishes.
	AfterQueue int64 `json:"afterQueue"`
	// AfterSelection additionally subtracts the current selection.
	AfterSelection int64 `json:"afterSelection"`
	// Headroom is AfterSelection minus the reserve: what remains usable for
	// further selections. Negative means the plan does not fit.
	Headroom int64 `json:"headroom"`
	// Status is "ok", "tight" (fits only if estimates hold) or "over".
	Status string `json:"status"`
	// Nominal is the likely advertised capacity (0 if unknown).
	Nominal  int64    `json:"nominal"`
	Messages []string `json:"messages"`
}

// Compute projects the volume's capacity. It never assumes space will be
// freed by deleting anything: replacing a resource always needs room for the
// old and new versions at the same time.
func Compute(in PlanInput) Plan {
	p := Plan{
		Total:             int64(in.Volume.TotalBytes),
		Available:         int64(in.Volume.AvailBytes),
		Installed:         in.Installed,
		Staged:            in.Staged,
		Queued:            max64(in.QueuedRemaining, 0),
		Selected:          max64(in.Selected, 0),
		SelectedUncertain: max64(in.SelectedUncertain, 0),
		Reserve:           max64(in.Reserve, 0),
	}
	p.Other = max64(p.Total-p.Available-p.Installed-p.Staged, 0)
	p.AfterQueue = p.Available - p.Queued
	p.AfterSelection = p.AfterQueue - p.Selected
	p.Headroom = p.AfterSelection - p.Reserve
	if n, ok := NominalCapacity(in.Volume.TotalBytes); ok {
		p.Nominal = int64(n)
	}

	switch {
	case p.Headroom < 0:
		p.Status = "over"
		p.Messages = append(p.Messages, fmt.Sprintf(
			"The current selection needs %s more than the space available while keeping the %s reserve.",
			FormatBytes(-p.Headroom), FormatBytes(p.Reserve)))
	case p.SelectedUncertain > 0 && p.Headroom < p.SelectedUncertain/4:
		// Estimated sizes can be off; warn if the margin is thin relative
		// to the uncertain part of the selection.
		p.Status = "tight"
		p.Messages = append(p.Messages, fmt.Sprintf(
			"The selection fits, but %s of it is estimated. Actual sizes may exceed the remaining %s.",
			FormatBytes(p.SelectedUncertain), FormatBytes(p.Headroom)))
	default:
		p.Status = "ok"
	}
	if p.AfterQueue-p.Reserve < 0 {
		p.Messages = append(p.Messages,
			"Queued downloads alone exceed the available space. Downloads will pause before the reserve is reached.")
	}
	if p.Nominal > 0 && p.Nominal != p.Total {
		p.Messages = append(p.Messages, fmt.Sprintf(
			"Drive likely sold as %s; the operating system reports %s (%s) after formatting and binary units.",
			FormatBytes(p.Nominal), FormatBytes(p.Total), FormatBinary(p.Total)))
	}
	return p
}

// CheckWrite reports whether n more bytes can be written to the volume while
// preserving reserve bytes, given other bytes already promised to running
// downloads.
func CheckWrite(availBytes uint64, n, promised, reserve int64) error {
	free := int64(availBytes) - promised - reserve
	if n > free {
		return &InsufficientSpaceError{Needed: n, Free: max64(free, 0), Reserve: reserve}
	}
	return nil
}

// InsufficientSpaceError explains why a write was refused.
type InsufficientSpaceError struct {
	Needed, Free, Reserve int64
}

func (e *InsufficientSpaceError) Error() string {
	return fmt.Sprintf("not enough space: needs %s, %s usable after keeping the %s reserve",
		FormatBytes(e.Needed), FormatBytes(e.Free), FormatBytes(e.Reserve))
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
