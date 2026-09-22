package conversion

// Catalog is the global conversion-policy catalog: the three fixed policy slots
// and the custom classifier tag library. Both are edited from the settings pages
// rather than from a workset, and both hold facts only the conversion rules can
// judge — what a policy must contain, what a classifier tag means — so the
// validation lives here and storage is reached through the ports below it.
type Catalog struct {
	slots PolicySlotStore
	tags  ClassifierTagStore
}

// NewCatalog wires the catalog to its storage.
func NewCatalog(slots PolicySlotStore, tags ClassifierTagStore) *Catalog {
	return &Catalog{slots: slots, tags: tags}
}
