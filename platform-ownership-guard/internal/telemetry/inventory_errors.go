package telemetry

import (
	"github.com/T2Dzung/coffee-shop/platform-ownership-guard/internal/inventory"
)

// RecordSnapshotInventoryErrors scans a snapshot and top-level error, attributing distinct (source, reason) error pairs.
func RecordSnapshotInventoryErrors(m *Metrics, snapshot *inventory.NormalizedSnapshot, collectErr error) {
	if m == nil {
		return
	}

	seen := make(map[string]bool)

	recordPair := func(source, reason string) {
		if source == "" || reason == "" {
			return
		}
		key := source + "|" + reason
		if !seen[key] {
			seen[key] = true
			m.RecordInventoryError(source, reason)
		}
	}

	if collectErr != nil {
		if invErr, ok := collectErr.(*inventory.InventoryError); ok {
			recordPair(SourceCollector, string(invErr.DTO.Class))
		} else {
			recordPair(SourceCollector, string(inventory.ErrTransientReadFailure))
		}
	}

	if snapshot == nil {
		return
	}

	if snapshot.ArgoDiscoveryError != nil {
		recordPair(SourceDiscovery, string(snapshot.ArgoDiscoveryError.Class))
	}

	for _, app := range snapshot.Applications {
		if app.Metadata.SourceError != nil {
			recordPair(SourceApplication, string(app.Metadata.SourceError.Class))
		} else if app.Metadata.Freshness == inventory.FreshnessStale {
			recordPair(SourceApplication, string(inventory.ErrStaleEvidence))
		}
	}

	for _, prot := range snapshot.Protections {
		if prot.Metadata.SourceError != nil {
			recordPair(SourceTarget, string(prot.Metadata.SourceError.Class))
		}
	}

	for _, owner := range snapshot.Owners {
		if owner.Metadata.SourceError != nil {
			recordPair(SourceOwner, string(owner.Metadata.SourceError.Class))
		}
	}
}
